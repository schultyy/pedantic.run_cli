package main

import (
	_ "embed"
	"encoding/json"
	"log"
)

// featuresJSON is the build-time feature configuration. It is baked into the
// binary at compile time via go:embed, so whatever features.json says when you
// build is frozen into that binary — edit the file and rebuild to change what
// ships. There is no runtime override on purpose: this gates unfinished work
// out of releases, not user-tunable behavior.
//
//go:embed features.json
var featuresJSON []byte

// Features toggles work-in-progress surfaces. Every feature defaults to off (the
// zero value), so an unfinished feature can only ship enabled if features.json
// explicitly turns it on — forgetting to set it can't accidentally expose it.
type Features struct {
	// DataPrime shows the DataPrime analysis tab. Kept off in released builds
	// until the DataPrime analyzer is ready for users.
	DataPrime bool `json:"dataprime"`
}

// features is the parsed configuration, loaded once at startup.
var features = loadFeatures()

func loadFeatures() Features {
	var f Features
	if err := json.Unmarshal(featuresJSON, &f); err != nil {
		// features.json is embedded at compile time, so malformed JSON is a
		// build mistake — fail loudly rather than silently shipping with every
		// feature defaulted off.
		log.Fatalf("parsing embedded features.json: %v", err)
	}
	return f
}
