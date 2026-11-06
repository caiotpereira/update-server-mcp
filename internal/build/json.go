// Copyright (c) 2026 Caio Pereira
// SPDX-License-Identifier: MIT

package build

import (
	"encoding/json"
	"io"
	"net/http"
)

func decodeJSON(resp *http.Response, out any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}
