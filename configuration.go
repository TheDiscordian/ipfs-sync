package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

var (
	BasePathFlag     = flag.String("basepath", "/ipfs-sync/", "relative MFS directory path")
	BasePath         string
	EndPointFlag     = flag.String("endpoint", "http://127.0.0.1:5001", "node to connect to over HTTP")
	EndPoint         string
	DirKeysFlag      = new(SyncDirs)
	DirKeys          []*DirKey
	SyncTimeFlag     = flag.Duration("sync", time.Second*10, "time to sleep between IPNS syncs (ex: 120s)")
	SyncTime         time.Duration
	TimeoutTimeFlag  = flag.Duration("timeout", time.Second*30, "longest time to wait for API calls like 'version' and 'files/mkdir' (ex: 60s)")
	TimeoutTime      time.Duration
	ConfigFileFlag   = flag.String("config", "", "path to config file to use")
	ConfigFile       string
	IgnoreFlag       = new(IgnoreStruct)
	Ignore           []string
	LicenseFlag      = flag.Bool("copyright", false, "display copyright and exit")
	DBPathFlag       = flag.String("db", "", `path to file where db should be stored (example: "/home/user/.ipfs-sync.db")`)
	DBPath           string
	IgnoreHiddenFlag = flag.Bool("ignorehidden", false, `ignore anything prefixed with "."`)
	IgnoreHidden     bool
	VersionFlag      = flag.Bool("version", false, "display version and exit")
	VerboseFlag      = flag.Bool("v", false, "display verbose output")
	Verbose          bool

	version string // passed by -ldflags
)

func init() {
	flag.Var(DirKeysFlag, "dirs", `set the dirs to monitor in json format like: [{"ID":"Example1", "Dir":"/home/user/Documents/", "Nocopy": false},{"ID":"Example2", "Dir":"/home/user/Pictures/", "Nocopy": false}]`)
	flag.Var(IgnoreFlag, "ignore", `set the suffixes to ignore (default: ["kate-swp", "swp", "part", "crdownload"])`)
}

//go:embed config.yaml.sample
var content embed.FS

// DirKey used for keeping track of directories, and it's used in the `dirs` config paramerter.
type DirKey struct {
	// config values
	ID       string `json:"ID" yaml:"ID"`
	Dir      string `yaml:"Dir"`
	Nocopy   bool   `yaml:"Nocopy"`
	DontHash bool   `yaml:"DontHash"`
	Pin      bool   `yaml:"Pin"`

	// probably best to let this be managed automatically
	CID     string
	MFSPath string
}

// SyncDirs is used for reading what the user specifies for which directories they'd like to sync.
type SyncDirs struct {
	DirKeys []*DirKey
	json    string
}

// Set takes a JSON string and marshals it into `sd`.
func (sd *SyncDirs) Set(str string) error {
	sd.DirKeys = make([]*DirKey, 0, 1)
	sd.json = str
	return json.Unmarshal([]byte(str), &sd.DirKeys)
}

// String returns the raw JSON used to build `sd`.
func (sd *SyncDirs) String() string {
	return sd.json
}

// IgnoreStruct is used for reading what the user specifies for which extensions they'd like to ignore.
type IgnoreStruct struct {
	Ignores []string
	json    string
}

// Set takes a JSON string and marshals it into `ig`.
func (ig *IgnoreStruct) Set(str string) error {
	ig.Ignores = make([]string, 0, 1)
	ig.json = str
	return json.Unmarshal([]byte(str), &ig.Ignores)
}

// String returns the raw JSON used to build `ig`.
func (ig *IgnoreStruct) String() string {
	return ig.json
}

// ConfigFileStruct is used for loading information from the config file.
type ConfigFileStruct struct {
	BasePath     string    `yaml:"BasePath"`
	EndPoint     string    `yaml:"EndPoint"`
	Dirs         []*DirKey `yaml:"Dirs"`
	Sync         string    `yaml:"Sync"`
	Ignore       []string  `yaml:"Ignore"`
	DB           string    `yaml:"DB"`
	IgnoreHidden bool      `yaml:"IgnoreHidden"`
	Timeout      string    `yaml:"Timeout"`
}

func loadConfig(path string) {
	log.Println("Loading config file", path)
	cfgFile, err := os.Open(path)
	if err != nil {
		log.Println("Config file not found, generating...")
		defaultconfig, _ := content.ReadFile("config.yaml.sample")
		err = ioutil.WriteFile(path, defaultconfig, 0644)
		if err != nil {
			log.Println("[ERROR] Error loading config file:", err)
			log.Println("[ERROR] Skipping config file...")
			return
		}
		cfgFile, err = os.Open(path)
		if err != nil {
			log.Println("[ERROR] Error loading config file:", err)
			log.Println("[ERROR] Skipping config file...")
			return
		}
	}
	defer cfgFile.Close()
	cfgTxt, _ := ioutil.ReadAll(cfgFile)

	cfg := new(ConfigFileStruct)
	err = yaml.Unmarshal(cfgTxt, cfg)
	if err != nil {
		log.Println("[ERROR] Error decoding config file:", err)
		log.Println("[ERROR] Skipping config file...")
		return
	}
	if cfg.BasePath != "" {
		BasePath = cfg.BasePath
	}
	if cfg.EndPoint != "" {
		EndPoint = cfg.EndPoint
	}
	if len(cfg.Dirs) > 0 {
		DirKeys = cfg.Dirs
	}
	if cfg.Sync != "" {
		tsTime, err := time.ParseDuration(cfg.Sync)
		if err != nil {
			log.Println("[ERROR] Error processing sync in config file:", err)
		} else {
			SyncTime = tsTime
		}
	}
	if cfg.Timeout != "" {
		tsTime, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			log.Println("[ERROR] Error processing timeout in config file:", err)
		} else {
			TimeoutTime = tsTime
		}
	}
	if cfg.DB != "" {
		DBPath = cfg.DB
	}
	IgnoreHidden = cfg.IgnoreHidden
}

// Process flags, and load config.
func ProcessFlags() {
	flag.Parse()
	if *LicenseFlag {
		fmt.Println("Copyright © 2020, The ipfs-sync Contributors. All rights reserved.")
		fmt.Println("BSD 3-Clause “New” or “Revised” License.")
		fmt.Println("License available at: https://github.com/TheDiscordian/ipfs-sync/blob/master/LICENSE")
		os.Exit(0)
	}
	if *VersionFlag {
		if version == "" {
			version = "devel"
		}
		fmt.Printf("ipfs-sync %s\n", version)
		os.Exit(0)
	}
	log.Println("ipfs-sync starting up...")

	ConfigFile = *ConfigFileFlag
	if ConfigFile != "" {
		loadConfig(ConfigFile)
	}
	if len(DirKeysFlag.DirKeys) > 0 {
		DirKeys = DirKeysFlag.DirKeys
	}

	// Process Dir
	if len(DirKeys) == 0 {
		log.Fatalln(`dirs field is required as flag, or in config.`)
	} else { // Check if Dir entries are at least somewhat valid.
		for _, dk := range DirKeys {
			if len(dk.Dir) == 0 {
				log.Fatalln("Dir entry path cannot be empty. (ID:", dk.ID, ")")
			}

			// Check if trailing "/" exists, if not, append it.
			if dk.Dir[len(dk.Dir)-1] != '/' {
				dk.Dir = dk.Dir + "/"
			}
		}
	}

	if *BasePathFlag != "/ipfs-sync/" || BasePath == "" {
		BasePath = *BasePathFlag
	}

	if *EndPointFlag != "http://127.0.0.1:5001" || EndPoint == "" {
		EndPoint = *EndPointFlag
	}

	// Ignore has no defaults so we need to set them here (if nothing else set it)
	if len(IgnoreFlag.Ignores) > 0 {
		Ignore = IgnoreFlag.Ignores
	} else if len(Ignore) == 0 {
		Ignore = []string{"kate-swp", "swp", "part", "crdownload"}
	}
	if *DBPathFlag != "" {
		DBPath = *DBPathFlag
	}
	if DBPath != "" {
		InitDB(DBPath)
	}
	if *SyncTimeFlag != time.Second*10 || SyncTime == 0 {
		SyncTime = *SyncTimeFlag
	}
	if *TimeoutTimeFlag != time.Second*30 || TimeoutTime == 0 {
		TimeoutTime = *TimeoutTimeFlag
	}
	if *IgnoreHiddenFlag {
		IgnoreHidden = true
	}
	Verbose = *VerboseFlag

	_, err := doRequest(TimeoutTime, "version")
	if err != nil {
		log.Fatalln("Failed to connect to end point:", err)
	}
}
