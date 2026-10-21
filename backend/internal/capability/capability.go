// Package capability describes the features available on an individual agent.
package capability

import (
	"encoding/json"
	"strings"
)

type Report struct {
	Screenshots    bool `json:"screenshots"`
	Recordings     bool `json:"recordings"`
	LiveScreen     bool `json:"live_screen"`
	InputCounts    bool `json:"input_counts"`
	BrowserDomains bool `json:"browser_domains"`
}

func JSON(r *Report) []byte {
	if r == nil {
		return nil
	}
	data, _ := json.Marshal(r)
	return data
}

// Legacy Linux agents predate reports. Unknown and older Windows agents cannot
// capture. An explicit report always overrides the legacy OS fallback.
func Live(os string, raw []byte) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return os == "" || strings.HasPrefix(os, "linux/")
	}
	var r Report
	return json.Unmarshal(raw, &r) == nil && r.LiveScreen
}
