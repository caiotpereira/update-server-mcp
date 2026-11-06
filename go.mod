module github.com/foundriesio/update-server-mcp

go 1.27.3

require (
	github.com/foundriesio/update-server v0.0.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/pelletier/go-toml v1.9.5
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/mattn/go-sqlite3 v1.14.32 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.11.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// The MCP server reuses update-server's own CLI client (cli/api, cli/config)
// so auth, contexts and response types stay in lockstep with the server it
// talks to. Built from the checkout next door rather than a published module.
replace github.com/foundriesio/update-server => ../update-server
