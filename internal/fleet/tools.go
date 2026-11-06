// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

// Package fleet exposes the update-server user API as MCP tools: devices,
// updates, rollouts and per-device app selection.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	toml "github.com/pelletier/go-toml"

	"github.com/foundriesio/update-server/cli/api"
)

// confirmErr is returned by every destructive tool called without confirm.
// Returned as a tool error so the model sees it and can ask the operator,
// rather than as a protocol failure.
func confirmErr(what string) error {
	return fmt.Errorf("refusing to %s without confirm=true; this cannot be undone", what)
}

// Register adds every fleet tool to the server. readOnly drops the tools that
// change server state, for operators who want a safe-to-browse server.
func Register(s *mcp.Server, a *api.Api, readOnly bool) {
	registerReads(s, a)
	if !readOnly {
		registerWrites(s, a)
	}
}

// --- reads ----------------------------------------------------------------

type devicesListIn struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"max devices to return (default 100)"`
	Page   int    `json:"page,omitempty" jsonschema:"1-based page number (default 1)"`
	SortBy string `json:"sort_by,omitempty" jsonschema:"server-side sort key, e.g. -last-seen"`
}

type devicesListOut struct {
	Devices    []api.DeviceListItem `json:"devices"`
	TotalPages int                  `json:"total_pages"`
	MorePages  bool                 `json:"more_pages"`
}

type uuidIn struct {
	Uuid string `json:"uuid" jsonschema:"device UUID"`
}

type updateIn struct {
	Update string `json:"update" jsonschema:"update name, as shown by updates_list"`
}

type rolloutIn struct {
	Update  string `json:"update" jsonschema:"update name"`
	Rollout string `json:"rollout" jsonschema:"rollout name"`
}

func registerReads(s *mcp.Server, a *api.Api) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "devices_list",
		Description: "List registered devices with the target each one reports and when it was last seen.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in devicesListIn) (*mcp.CallToolResult, devicesListOut, error) {
		if in.Limit <= 0 {
			in.Limit = 100
		}
		if in.Page <= 0 {
			in.Page = 1
		}
		devices, more, pages, err := a.Devices().ListPage(in.Page, in.Limit, in.SortBy)
		if err != nil {
			return nil, devicesListOut{}, err
		}
		return nil, devicesListOut{Devices: devices, TotalPages: pages, MorePages: more}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_show",
		Description: "Full detail for one device: labels, current target, apps, last seen.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in uuidIn) (*mcp.CallToolResult, *api.Device, error) {
		d, err := a.Devices().Get(in.Uuid)
		return nil, d, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_updates",
		Description: "The update ids this device has processed, newest first.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in uuidIn) (*mcp.CallToolResult, []string, error) {
		ids, err := a.Devices().Updates(in.Uuid)
		return nil, ids, err
	})

	type eventsIn struct {
		Uuid     string `json:"uuid" jsonschema:"device UUID"`
		UpdateId string `json:"update_id" jsonschema:"update id from device_updates"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_update_events",
		Description: "Per-step events for one update on one device — the first place to look when an update failed.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in eventsIn) (*mcp.CallToolResult, []api.DeviceUpdateEvent, error) {
		ev, err := a.Devices().UpdateEvents(in.Uuid, in.UpdateId)
		return nil, ev, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "device_apps_states",
		Description: "What the device last reported about each app: fetched, installed, running.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in uuidIn) (*mcp.CallToolResult, map[string]any, error) {
		var out map[string]any
		err := a.Get("/v1/devices/"+in.Uuid+"/apps-states", &out)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "device_applied_config",
		Description: "The config the device reports having applied, with the server's view of " +
			"what it should have. Use this to tell 'config not delivered yet' from 'config wrong'.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in uuidIn) (*mcp.CallToolResult, *api.AppliedConfigs, error) {
		res, err := a.Configs().Device(in.Uuid).GetApplied()
		return nil, &res, err
	})

	type configShowIn struct {
		Scope string `json:"scope" jsonschema:"one of factory, group, device"`
		Name  string `json:"name,omitempty" jsonschema:"group name or device UUID; omit for factory scope"`
	}
	type configFileOut struct {
		Name      string   `json:"name"`
		OnChanged []string `json:"on_changed,omitempty"`
		Encrypted bool     `json:"encrypted"`
		Value     string   `json:"value,omitempty" jsonschema:"contents; omitted for encrypted files"`
	}
	type configShowOut struct {
		CreatedAt string          `json:"created_at,omitempty"`
		Reason    string          `json:"reason,omitempty"`
		Files     []configFileOut `json:"files"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "config_show",
		Description: "The config file set at factory, group or device scope. " +
			"Encrypted file contents are withheld; only their names are reported.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in configShowIn) (*mcp.CallToolResult, configShowOut, error) {
		capi, err := specificConfigs(a, in.Scope, in.Name)
		if err != nil {
			return nil, configShowOut{}, err
		}
		set, err := capi.Get()
		if err != nil {
			return nil, configShowOut{}, err
		}
		out := configShowOut{Reason: set.Reason}
		for name, f := range set.Files {
			cf := configFileOut{Name: name, OnChanged: f.OnChanged, Encrypted: !f.Unencrypted}
			if f.Unencrypted {
				cf.Value = f.Value
			}
			out.Files = append(out.Files, cf)
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "groups_list",
		Description: "Device group names known to the server.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, []string, error) {
		names, err := a.Configs().ListGroups()
		return nil, names, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "updates_list",
		Description: "Every uploaded update with its tag, upload time and how many devices are on it.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, []api.Update, error) {
		updates, err := a.Updates().List()
		return nil, updates, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_summary",
		Description: "Rollout progress for an update: how many devices are queued, updating, succeeded or failed.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in updateIn) (*mcp.CallToolResult, api.UpdateSummary, error) {
		sum, err := a.Updates().GetSummary(in.Update)
		return nil, sum, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "update_tuf",
		Description: "The TUF metadata the server generated for an update, keyed by role file. " +
			"Use it to check the target's hardware-id, version, apps and ostree hash.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in updateIn) (*mcp.CallToolResult, api.UpdateTuf, error) {
		tuf, err := a.Updates().GetTuf(in.Update)
		return nil, tuf, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "rollouts_list",
		Description: "Rollout names created for an update.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in updateIn) (*mcp.CallToolResult, []string, error) {
		names, err := a.Updates().Get(in.Update)
		return nil, names, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "rollout_show",
		Description: "One rollout: the uuids and groups it targets, and the devices it actually affects.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in rolloutIn) (*mcp.CallToolResult, api.Rollout, error) {
		r, err := a.Updates().GetRollout(in.Update, in.Rollout)
		return nil, r, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "rollout_summary",
		Description: "Progress counts for a single rollout rather than the whole update.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in rolloutIn) (*mcp.CallToolResult, api.UpdateSummary, error) {
		sum, err := a.Updates().GetRolloutSummary(in.Update, in.Rollout)
		return nil, sum, err
	})

	type statusIn struct {
		Update  string `json:"update" jsonschema:"update name"`
		Status  string `json:"status" jsonschema:"status to filter on, as reported by update_summary"`
		Rollout string `json:"rollout,omitempty" jsonschema:"limit to one rollout"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "devices_for_status",
		Description: "Which devices are in a given update status — the drill-down behind update_summary counts.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, []string, error) {
		if in.Rollout != "" {
			uuids, err := a.Updates().GetDevicesForRolloutStatus(in.Update, in.Rollout, in.Status)
			return nil, uuids, err
		}
		uuids, err := a.Updates().GetDevicesForStatus(in.Update, in.Status)
		return nil, uuids, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "denied_devices_list",
		Description: "Devices on the denied list; they are refused at the gateway until removed.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, []string, error) {
		uuids, err := a.Devices().ListDenied()
		return nil, uuids, err
	})

	type pkiIn struct {
		Certs []string `json:"certs,omitempty" jsonschema:"cert names to fetch; defaults to root.crt"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "pki_cert",
		Description: "Server PKI certificates in PEM form. The TLS cert's SAN is the gateway name " +
			"devices are given at registration, so this is how to check that name.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in pkiIn) (*mcp.CallToolResult, map[string]string, error) {
		if len(in.Certs) == 0 {
			in.Certs = []string{"root.crt"}
		}
		certs, err := a.Pki().GetCerts(in.Certs)
		return nil, certs, err
	})
}

func specificConfigs(a *api.Api, scope, name string) (api.SpecificConfigsApi, error) {
	switch scope {
	case "factory", "":
		return a.Configs().Factory(), nil
	case "group":
		if name == "" {
			return nil, errors.New("group scope needs a group name")
		}
		return a.Configs().Group(name), nil
	case "device":
		if name == "" {
			return nil, errors.New("device scope needs a device UUID")
		}
		return a.Configs().Device(name), nil
	}
	return nil, fmt.Errorf("unknown scope %q: use factory, group or device", scope)
}

// --- writes ---------------------------------------------------------------

func registerWrites(s *mcp.Server, a *api.Api) {
	type rolloutCreateIn struct {
		Update  string   `json:"update" jsonschema:"update name to roll out"`
		Rollout string   `json:"rollout" jsonschema:"name for this rollout"`
		Uuids   []string `json:"uuids,omitempty" jsonschema:"device UUIDs to include"`
		Groups  []string `json:"groups,omitempty" jsonschema:"device groups to include"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "rollout_create",
		Description: "Make an update visible to devices. Until a device is in a rollout it is served " +
			"the default TUF metadata, which on a fresh server does not exist — the device then " +
			"reports a 404 on /repo/targets.json that looks like a DNS or TLS fault.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in rolloutCreateIn) (*mcp.CallToolResult, map[string]string, error) {
		if len(in.Uuids) == 0 && len(in.Groups) == 0 {
			return nil, nil, errors.New("give at least one uuid or group")
		}
		err := a.Updates().CreateRollout(in.Update, in.Rollout, api.Rollout{Uuids: in.Uuids, Groups: in.Groups})
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"update": in.Update, "rollout": in.Rollout, "status": "created"}, nil
	})

	type selectAppsIn struct {
		Scope  string `json:"scope" jsonschema:"one of factory, group, device"`
		Name   string `json:"name,omitempty" jsonschema:"group name or device UUID; omit for factory scope"`
		Apps   string `json:"apps,omitempty" jsonschema:"comma-separated app names; \",\" for no apps; \"-\" to clear the override"`
		Tag    string `json:"tag,omitempty" jsonschema:"tag to follow; \"-\" to clear the override"`
		Reason string `json:"reason,omitempty" jsonschema:"stored as the reason for this change"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "select_apps",
		Description: "Choose which compose apps a device, group or the whole factory runs, and/or the tag " +
			"it follows. This writes z-50-fioctl.toml, the same file `fiocli configs updates` writes, " +
			"and REPLACES the previous value rather than merging. An app must already be in the " +
			"device's target or there is nothing for it to fetch.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in selectAppsIn) (*mcp.CallToolResult, selectAppsOut, error) {
		return selectApps(a, in.Scope, in.Name, in.Apps, in.Tag, in.Reason)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "update_upload",
		Description: "Upload an offline-update directory as a new update. For an apps-only payload " +
			"(no ostree_repo) the server cannot probe metadata, so hardware_id and ostree_hash are " +
			"required; keep ostree_hash identical across uploads so devices never see a platform change.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in uploadIn) (*mcp.CallToolResult, uploadOut, error) {
		return upload(ctx, a, in)
	})

	type updateDeleteIn struct {
		Update  string `json:"update" jsonschema:"update name to delete"`
		Confirm bool   `json:"confirm" jsonschema:"must be true; deleting an update cannot be undone"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_delete",
		Description: "Delete an update and its content. Devices already on it keep running; they just lose the source.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in updateDeleteIn) (*mcp.CallToolResult, map[string]string, error) {
		if !in.Confirm {
			return nil, nil, confirmErr("delete update " + in.Update)
		}
		if err := a.Updates().Delete(in.Update); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"update": in.Update, "status": "deleted"}, nil
	})

	type deviceDeleteIn struct {
		Uuid    string `json:"uuid" jsonschema:"device UUID"`
		Confirm bool   `json:"confirm" jsonschema:"must be true; the device must re-register afterwards"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name: "device_delete",
		Description: "Remove a device record. The device keeps its client certificate but is unknown to " +
			"the server, so it has to be registered again.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in deviceDeleteIn) (*mcp.CallToolResult, map[string]string, error) {
		if !in.Confirm {
			return nil, nil, confirmErr("delete device " + in.Uuid)
		}
		if err := a.Devices().Delete(in.Uuid); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"uuid": in.Uuid, "status": "deleted"}, nil
	})

	type undenyIn struct {
		Uuid    string `json:"uuid" jsonschema:"device UUID to remove from the denied list"`
		Confirm bool   `json:"confirm" jsonschema:"must be true; this lets the device talk to the gateway again"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "denied_device_delete",
		Description: "Take a device off the denied list so it can reach the gateway again.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, in undenyIn) (*mcp.CallToolResult, map[string]string, error) {
		if !in.Confirm {
			return nil, nil, confirmErr("un-deny device " + in.Uuid)
		}
		if err := a.Devices().DeleteDenied(in.Uuid); err != nil {
			return nil, nil, err
		}
		return nil, map[string]string{"uuid": in.Uuid, "status": "removed from denied list"}, nil
	})
}

type selectAppsOut struct {
	Changed bool   `json:"changed"`
	File    string `json:"file" jsonschema:"the config file written"`
	Content string `json:"content" jsonschema:"the resulting file, so the caller sees what the device will get"`
}

// selectApps mirrors `fiocli configs updates`: it edits the single
// z-50-fioctl.toml file in the scope's config set, leaving every other file
// alone, and reports the resulting file so the caller can see exactly what
// the device will get.
func selectApps(a *api.Api, scope, name, apps, tag, reason string) (*mcp.CallToolResult, selectAppsOut, error) {
	const (
		sotaOverride = "z-50-fioctl.toml"
		onChanged    = "/usr/share/fioconfig/handlers/aktualizr-toml-update"
	)
	var zero selectAppsOut

	apps, tag = strings.TrimSpace(apps), strings.TrimSpace(tag)
	if apps == "" && tag == "" {
		return nil, zero, errors.New("give apps, tag, or both")
	}
	if reason == "" {
		reason = "Override update configuration via MCP"
	}

	capi, err := specificConfigs(a, scope, name)
	if err != nil {
		return nil, zero, err
	}
	set, err := capi.Get()
	if err != nil {
		return nil, zero, err
	}
	if set.Files == nil {
		set.Files = make(map[string]api.ConfigFile)
	}

	sota, err := toml.Load(set.Files[sotaOverride].Value)
	if err != nil {
		return nil, zero, fmt.Errorf("existing %s is not valid TOML: %w", sotaOverride, err)
	}

	switch apps {
	case "": // leave as-is
	case "-":
		if sota.Has("pacman.compose_apps") {
			if err := sota.Delete("pacman.compose_apps"); err != nil {
				return nil, zero, err
			}
		}
	case ",":
		sota.Set("pacman.compose_apps", "")
	default:
		sota.Set("pacman.compose_apps", apps)
	}

	switch tag {
	case "": // leave as-is
	case "-":
		if sota.Has("pacman.tags") {
			if err := sota.Delete("pacman.tags"); err != nil {
				return nil, zero, err
			}
		}
	default:
		sota.Set("pacman.tags", tag)
	}

	content, err := sota.ToTomlString()
	if err != nil {
		return nil, zero, err
	}
	out := selectAppsOut{File: sotaOverride, Content: content}
	if content == set.Files[sotaOverride].Value {
		return nil, out, nil // Changed stays false
	}
	set.Files[sotaOverride] = api.ConfigFile{Value: content, Unencrypted: true, OnChanged: []string{onChanged}}
	if err := capi.Put(api.ConfigFileSet{Files: set.Files, Reason: reason}); err != nil {
		return nil, zero, err
	}
	out.Changed = true
	return nil, out, nil
}
