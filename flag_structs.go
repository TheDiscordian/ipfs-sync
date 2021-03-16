package main

import (
	"encoding/json"
)

type DirKey struct {
	Key    string `json:"ID"`
	Dir    string
	Nocopy bool

	// probably best to let this be managed automatically
	CID     string
	MFSPath string
}

type SyncDirs struct {
	DirKeys []*DirKey
	json    string
}

func (sd *SyncDirs) Set(str string) error {
	sd.DirKeys = make([]*DirKey, 0, 1)
	sd.json = str
	return json.Unmarshal([]byte(str), &sd.DirKeys)
}

func (sd *SyncDirs) String() string {
	return sd.json
}

type IgnoreStruct struct {
	Ignores []string
	json    string
}

func (ig *IgnoreStruct) Set(str string) error {
	ig.Ignores = make([]string, 0, 1)
	ig.json = str
	return json.Unmarshal([]byte(str), &ig.Ignores)
}

func (ig *IgnoreStruct) String() string {
	return ig.json
}
