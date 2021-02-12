package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	KeySpace = "ipfs-sync."
	API      = "/api/v0/"
)

// TODO toggle to ignore hidden files

var (
	BasePathFlag   = flag.String("basepath", "/ipfs-sync/", "relative MFS directory path")
	BasePath       string
	EndPointFlag   = flag.String("endpoint", "http://127.0.0.1:5001", "node to connect to over HTTP")
	EndPoint       string
	DirKeysFlag    = new(SyncDirs)
	DirKeys        []*DirKey
	SyncTimeFlag   = flag.Duration("sync", time.Second*10, "time to sleep between IPNS syncs (ex: 120s)")
	SyncTime       time.Duration
	ConfigFileFlag = flag.String("config", "", "path to config file to use")
	ConfigFile     string
	IgnoreFlag     = new(IgnoreStruct)
	Ignore         []string
	LicenseFlag    = flag.Bool("license", false, "display license and exit")
)

func init() {
	flag.Var(DirKeysFlag, "dirs", `set the dirs to monitor in json format like: [{"Key":"Example1", "Dir":"/home/user/Documents/"},{"Key":"Example2", "Dir":"/home/user/Pictures/"}]`)
	flag.Var(IgnoreFlag, "ignore", `set the suffixes to ignore (default: ["kate-swp", "swp", "part"])`)
}

func findInStringSlice(slice []string, val string) int {
	for i, item := range slice {
		if item == val {
			return i
		}
	}
	return -1
}

func watchDir(dir string) chan bool {
	dirSplit := strings.Split(dir, "/")
	dirName := dirSplit[len(dirSplit)-2]

	// creates a new file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println("ERROR", err)
		return nil
	}

	watchThis := func(path string, fi os.FileInfo, err error) error {
		// since fsnotify can watch all the files in a directory, watchers only need to be added to each nested directory
		if fi.Mode().IsDir() {
			return watcher.Add(path)
		}

		return nil
	}

	// starting at the root of the project, walk each file/directory searching for directories
	if err := filepath.Walk(dir, watchThis); err != nil {
		log.Println("ERROR", err)
	}

	done := make(chan bool, 1)

	go func() {
		defer watcher.Close()
		for {
			select {
			// watch for events
			case event, ok := <-watcher.Events:
				if !ok {
					log.Println("NOT OK")
					return
				}
				//log.Println("event:", event)
				splitName := strings.Split(event.Name, ".")
				if findInStringSlice(Ignore, splitName[len(splitName)-1]) > -1 {
					continue
				}
				switch event.Op {
				case fsnotify.Create:
					fi, err := os.Stat(event.Name)
					if err != nil {
						log.Println("WATCHER ERROR", err)
						continue
					} else if !fi.Mode().IsDir() {
						repl, err := AddFile(event.Name, dirName+"/"+event.Name[len(dir):])
						if err != nil {
							log.Println("WATCHER ERROR", err)
						}
						if repl != "" {
							log.Println(repl)
						}
						continue
					}
					if err := filepath.Walk(event.Name, watchThis); err != nil {
						log.Println("ERROR", err)
					}
				case fsnotify.Write:
					repl, err := AddFile(event.Name, dirName+"/"+event.Name[len(dir):])
					if err != nil {
						log.Println("ERROR", err)
					}
					if repl != "" {
						log.Println(repl)
					}
				case fsnotify.Remove, fsnotify.Rename:
					err := RemoveFile(dirName + "/" + event.Name[len(dir):])
					if err != nil {
						log.Println("ERROR", err)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					log.Println("WATCHER NOT OK")
					return
				}
				log.Println("error:", err)
			case <-done:
				return
			}
		}
	}()

	return done
}

func doRequest(cmd string) (string, error) {
	c := &http.Client{}
	req, err := http.NewRequest("POST", EndPoint+API+cmd, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

// HashStruct is useful when you only care about the returned hash.
type HashStruct struct {
	Hash string
}

// GetFileCID gets a file CID based on MFS path relative to BasePath.
func GetFileCID(filePath string) string {
	out, _ := doRequest("files/stat?hash=true&arg=" + BasePath + url.QueryEscape(filePath))

	fStat := new(HashStruct)

	err := json.Unmarshal([]byte(out), &fStat)
	if err != nil {
		return ""
	}
	return fStat.Hash
}

// RemoveFile removes a file from the MFS relative to BasePath.
func RemoveFile(fpath string) error {
	log.Println("Removing", fpath, "...")
	repl, err := doRequest(fmt.Sprintf(`files/rm?arg=%s&force=true`, BasePath+url.QueryEscape(fpath)))
	if err != nil || repl != "" {
		if repl != "" {
			err = errors.New(repl)
		}
		return err
	}
	return err
}

func filePathWalkDir(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// AddDir adds a directory, and returns CID.
func AddDir(path string) (string, error) {
	pathSplit := strings.Split(path, "/")
	dirName := pathSplit[len(pathSplit)-2]
	files, err := filePathWalkDir(path)
	if err != nil {
		return "", err
	}
	for _, file := range files {
		repl, err := AddFile(file, dirName+"/"+file[len(path):])
		if err != nil || repl != "" {
			if repl != "" {
				err = errors.New(repl)
			}
			return "", err
		}
	}
	cid := GetFileCID(dirName)
	err = Pin(cid)
	return cid, err
}

// AddFile adds a file to the MFS relative to BasePath. from should be the full path to the file intended to be added.
func AddFile(from, to string) (string, error) {
	to = BasePath + to
	log.Println("Adding file to", to, "...")
	f, err := os.Open(from)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buff := &bytes.Buffer{}
	writer := multipart.NewWriter(buff)
	part, _ := writer.CreateFormFile("file", filepath.Base(f.Name()))
	io.Copy(part, f)
	writer.Close()

	c := &http.Client{}
	req, err := http.NewRequest("POST", fmt.Sprintf(EndPoint+API+`files/write?arg=%s&create=true&truncate=true&parents=true`, url.QueryEscape(to)), buff)
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), err
}

// Pin CID
func Pin(cid string) error {
	_, err := doRequest("pin/add?arg=" + url.QueryEscape(cid))
	return err
}

// ErrorStruct allows us to read the errors received by the IPFS daemon.
type ErrorStruct struct {
	Message string
	Code    int
	Type    string
}

// UpdatePin updates a recursive pin to a new CID, unpinning old content.
func UpdatePin(from, to string) {
	resp, err := doRequest("pin/update?arg=" + url.QueryEscape(from) + "&arg=" + url.QueryEscape(to))
	if err != nil {
		log.Println("[ERROR]", err)
		return
	}
	resperr := new(ErrorStruct)
	err = json.Unmarshal([]byte(resp), resperr)
	if err != nil {
		log.Println("[ERROR]", err)
		return
	}
	if resperr.Type == "error" {
		log.Println("Error updating pin:", resperr.Message)
		err = Pin(to)
		if err != nil {
			log.Println("[ERROR] Error adding pin:", err)
		}
	}
}

// Key contains information about an IPNS key.
type Key struct {
	Id   string
	Name string
}

// Keys is used to store a slice of Key.
type Keys struct {
	Keys []Key
}

// ListKeys lists all the keys in the IPFS daemon.
// TODO Only return keys in the namespace.
func ListKeys() *Keys {
	res, err := doRequest("key/list")
	if err != nil {
		log.Println("[ERROR]", err)
		return nil
	}
	keys := new(Keys)
	err = json.Unmarshal([]byte(res), keys)
	if err != nil {
		log.Println("[ERROR]", err)
		return nil
	}
	return keys
}

// ResolveIPNS takes an IPNS key and returns the CID it resolves to.
func ResolveIPNS(key string) string {
	res, err := doRequest("name/resolve?arg=" + key)
	if err != nil {
		log.Println("[ERROR]", err)
		return ""
	}
	type PathStruct struct {
		Path string
	}
	path := new(PathStruct)
	err = json.Unmarshal([]byte(res), path)
	if err != nil {
		log.Println("[ERROR]", err)
		return ""
	}
	return strings.Split(path.Path, "/")[2]
}

// Generates an IPNS key in the keyspace based on name.
func GenerateKey(name string) Key {
	res, err := doRequest("key/gen?arg=" + KeySpace + name)
	if err != nil {
		log.Panicln("[ERROR]", err)
	}
	key := new(Key)
	err = json.Unmarshal([]byte(res), key)
	if err != nil {
		log.Panicln("[ERROR]", err)
	}
	return *key
}

// Publish CID to IPNS
func Publish(cid, key string) error {
	_, err := doRequest(fmt.Sprintf("name/publish?arg=%s&key=%s", url.QueryEscape(cid), KeySpace+key))
	// log.Println("[DEBUG] Publish:", res)
	return err
}

// WatchDog watches for directory updates, periodically updates IPNS records, and updates recursive pins.
func WatchDog() {
	// Init WatchDog
	keys := ListKeys()
	for _, dk := range DirKeys {
		found := false
		splitPath := strings.Split(dk.Dir, "/")
		dk.MFSPath = splitPath[len(splitPath)-2]
		for _, ik := range keys.Keys {
			if ik.Name == KeySpace+dk.Key {
				dk.CID = ResolveIPNS(ik.Id)
				found = true
				log.Println(dk.Key, "loaded:", ik.Id)
				watchDir(dk.Dir)
				break
			}
		}
		if found {
			continue
		}
		log.Println(dk.Key, "not found, generating...")
		ik := GenerateKey(dk.Key)
		var err error
		dk.CID, err = AddDir(dk.Dir)
		if err != nil {
			log.Panicln("[ERROR] Failed to add directory:", err)
		}
		Publish(dk.CID, dk.Key)
		log.Println(dk.Key, "loaded:", ik.Id)
		watchDir(dk.Dir)
	}

	// Main loop
	for {
		time.Sleep(SyncTime)
		for _, dk := range DirKeys {
			if fCID := GetFileCID(dk.MFSPath); fCID != dk.CID {
				// log.Printf("[DEBUG] '%s' != '%s'", fCID, dk.CID)
				Publish(fCID, dk.Key)
				UpdatePin(dk.CID, fCID)
				dk.CID = fCID
				log.Println(dk.MFSPath, "updated...")
			}
		}
	}
}

// ConfigFileStruct is used for loading information from the config file.
type ConfigFileStruct struct {
	BasePath string
	EndPoint string
	Dirs     []*DirKey
	Sync     string
	Ignore   []string
}

func loadConfig(path string) {
	cfgFile, err := os.Open(path)
	if err != nil {
		log.Println("[ERROR] Error loading config file:", err)
		log.Println("[ERROR] Skipping config file...")
		return
	}
	defer cfgFile.Close()

	cfg := new(ConfigFileStruct)
	dec := json.NewDecoder(cfgFile)
	err = dec.Decode(cfg)
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
}

func main() {
	// Process config and flags.
	flag.Parse()
	if *LicenseFlag {
		log.Println("Copyright © 2020, The ipfs-sync Contributors. All rights reserved.")
		return
	}
	log.Println("ipfs-sync starting up...")

	ConfigFile = *ConfigFileFlag
	if ConfigFile != "" {
		loadConfig(ConfigFile)
	}
	if len(DirKeysFlag.DirKeys) > 0 {
		DirKeys = DirKeysFlag.DirKeys
	}
	if len(DirKeys) == 0 {
		log.Fatalln("-dirs field is required.")
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
		Ignore = []string{"kate-swp", "swp", "part"}
	}
	if *SyncTimeFlag != time.Second*10 || SyncTime == 0 {
		SyncTime = *SyncTimeFlag
	}

	// Start WatchDog.
	WatchDog()
}