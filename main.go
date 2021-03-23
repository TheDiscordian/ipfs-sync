package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
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

	DefaultConfig = `{
	"BasePath": "/ipfs-sync/",
	"EndPoint": "http://127.0.0.1:5001",
	"Dirs": [],
	"Sync": "10s",
	"Ignore": ["kate-swp", "swp", "part"],
	"DB": "",
	"IgnoreHidden": true
}`
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
	ConfigFileFlag   = flag.String("config", "", "path to config file to use")
	ConfigFile       string
	IgnoreFlag       = new(IgnoreStruct)
	Ignore           []string
	LicenseFlag      = flag.Bool("copyright", false, "display copyright and exit")
	DBPathFlag       = flag.String("db", "", `path to file where db should be stored (example: "/home/user/.ipfs-sync/hashes.db")`)
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

func watchDir(dir string, nocopy bool) chan bool {
	dirSplit := strings.Split(dir, "/")
	dirName := dirSplit[len(dirSplit)-2]

	localDirs := make(map[string]bool)

	// creates a new file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Println("ERROR", err)
		return nil
	}

	watchThis := func(path string, fi fs.DirEntry, err error) error {
		// since fsnotify can watch all the files in a directory, watchers only need to be added to each nested directory
		// we must check for nil as a panic is possible if fi is for some reason nil
		if fi != nil && fi.IsDir() {
			filePathSplit := strings.Split(path, "/")
			if IgnoreHidden {
				if len(filePathSplit[len(filePathSplit)-1]) > 0 {
					if filePathSplit[len(filePathSplit)-1][0] == '.' {
						return fs.SkipDir
					}
				} else {
					if filePathSplit[len(filePathSplit)-2][0] == '.' {
						return fs.SkipDir
					}
				}
			}
			return watcher.Add(path)
		}

		return nil
	}

	addFile := func(fname string, overwrite bool) {
		splitName := strings.Split(fname, "/")
		parentDir := strings.Join(splitName[:len(splitName)-1], "/")
		makeDir := !localDirs[parentDir]
		if makeDir {
			localDirs[parentDir] = true
		}
		repl, err := AddFile(fname, dirName+"/"+fname[len(dir):], nocopy, makeDir, overwrite)
		if err != nil {
			log.Println("WATCHER ERROR", err)
		}
		if repl != "" {
			log.Println(repl)
		}
		if Hashes != nil {
			HashLock.Lock()
			if Hashes[fname] != nil {
				Hashes[fname].Recalculate(fname)
			} else {
				Hashes[fname] = new(FileHash).Recalculate(fname)
			}
			Hashes[fname].Update()
			HashLock.Unlock()
		}
	}

	addDir := func(path string, fi fs.DirEntry, err error) error {
		if fi != nil && fi.IsDir() {
			filePathSplit := strings.Split(path, "/")
			if IgnoreHidden {
				if len(filePathSplit[len(filePathSplit)-1]) > 0 {
					if filePathSplit[len(filePathSplit)-1][0] == '.' {
						return fs.SkipDir
					}
				} else {
					if filePathSplit[len(filePathSplit)-2][0] == '.' {
						return fs.SkipDir
					}
				}
			}
			return nil
		} else {
			addFile(path, false)
		}

		return nil
	}

	// starting at the root of the project, walk each file/directory searching for directories
	if err := filepath.WalkDir(dir, watchThis); err != nil {
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
				if Verbose {
					log.Println("fsnotify event:", event)
				}
				if len(event.Name) == 0 {
					continue
				}
				filePathSplit := strings.Split(event.Name, "/")
				if IgnoreHidden {
					if len(filePathSplit[len(filePathSplit)-1]) > 0 {
						if filePathSplit[len(filePathSplit)-1][0] == '.' {
							continue
						}
					} else {
						if filePathSplit[len(filePathSplit)-2][0] == '.' {
							continue
						}
					}
				}
				splitName := strings.Split(event.Name, ".")
				if findInStringSlice(Ignore, splitName[len(splitName)-1]) > -1 {
					continue
				}
				switch event.Op {
				case fsnotify.Create:
					fi, err := os.Stat(event.Name)
					if err != nil {
						log.Println("WATCHER ERROR", err)
					} else if !fi.Mode().IsDir() {
						addFile(event.Name, true)
					} else if err := filepath.WalkDir(event.Name, watchThis); err == nil {
						filepath.WalkDir(event.Name, addDir)
					} else {
						log.Println("ERROR", err)
					}
				case fsnotify.Write:
					addFile(event.Name, true)
				case fsnotify.Remove, fsnotify.Rename:
					// check if file is *actually* gone
					_, err := os.Stat(event.Name)
					if err == nil {
						continue
					}
					// remove watcher, just in case it's a directory
					watcher.Remove(event.Name)
					if localDirs[event.Name] {
						delete(localDirs, event.Name)
					}
					fpath := dirName + "/" + event.Name[len(dir):]
					log.Println("Removing", fpath, "...")
					err = RemoveFile(fpath)
					if err != nil {
						log.Println("ERROR", err)
					}
					if Hashes != nil {
						HashLock.Lock()
						Hashes[event.Name].Delete(event.Name)
						HashLock.Unlock()
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
	out, _ := doRequest("files/stat?hash=true&arg=" + url.QueryEscape(BasePath+filePath))

	fStat := new(HashStruct)

	err := json.Unmarshal([]byte(out), &fStat)
	if err != nil {
		return ""
	}
	return fStat.Hash
}

// RemoveFile removes a file from the MFS relative to BasePath.
func RemoveFile(fpath string) error {
	repl, err := doRequest(fmt.Sprintf(`files/rm?arg=%s&force=true`, url.QueryEscape(BasePath+fpath)))
	if err != nil || repl != "" {
		if repl != "" {
			err = errors.New(repl)
		}
		return err
	}
	return err
}

// MakeDir makes a directory along with parents in path
func MakeDir(path string) error {
	repl, err := doRequest(fmt.Sprintf(`files/mkdir?arg=%s&parents=true`, url.QueryEscape(BasePath+path)))
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
	err := filepath.WalkDir(root, func(path string, info fs.DirEntry, err error) error {
		if !info.IsDir() {
			filePathSplit := strings.Split(path, "/")
			if IgnoreHidden && filePathSplit[len(filePathSplit)-1][0] == '.' {
				return nil
			}
			files = append(files, path)
		} else {
			dirPathSplit := strings.Split(path, "/")
			if IgnoreHidden && len(dirPathSplit[len(dirPathSplit)-1]) > 0 && dirPathSplit[len(dirPathSplit)-1][0] == '.' {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return files, err
}

// AddDir adds a directory, and returns CID.
func AddDir(path string, nocopy bool) (string, error) {
	pathSplit := strings.Split(path, "/")
	dirName := pathSplit[len(pathSplit)-2]
	files, err := filePathWalkDir(path)
	if err != nil {
		return "", err
	}
	localDirs := make(map[string]bool)
	for _, file := range files {
		filePathSplit := strings.Split(file, "/")
		if IgnoreHidden && filePathSplit[len(filePathSplit)-1][0] == '.' {
			continue
		}
		splitName := strings.Split(file, ".")
		if findInStringSlice(Ignore, splitName[len(splitName)-1]) > -1 {
			continue
		}
		parentDir := strings.Join(filePathSplit[:len(filePathSplit)-1], "/")
		makeDir := !localDirs[parentDir]
		if makeDir {
			localDirs[parentDir] = true
		}
		repl, err := AddFile(file, dirName+"/"+file[len(path):], nocopy, makeDir, false)
		if err != nil || repl != "" {
			//if repl != "" { FIXME check if resp is really an error before deciding it is
			//	err = errors.New(repl)
			//}
			return "", err
		}
	}
	cid := GetFileCID(dirName)
	err = Pin(cid)
	return cid, err
}

// AddFile adds a file to the MFS relative to BasePath. from should be the full path to the file intended to be added.
// If makedir is true, it'll create the directory it'll be placed in.
// If overwrite is true, it'll perform an rm before copying to MFS.
func AddFile(from, to string, nocopy bool, makedir bool, overwrite bool) (string, error) {
	log.Println("Adding file to", BasePath+to, "...")
	f, err := os.Open(from)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buff := &bytes.Buffer{}
	writer := multipart.NewWriter(buff)

	h := make(textproto.MIMEHeader)
	h.Set("Abspath", from)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "file", url.QueryEscape(f.Name())))
	h.Set("Content-Type", "application/octet-stream")
	part, _ := writer.CreatePart(h)
	if Verbose {
		log.Println("Generating file headers...")
	}
	io.Copy(part, f)

	writer.Close()

	c := &http.Client{}
	req, err := http.NewRequest("POST", EndPoint+API+fmt.Sprintf(`add?nocopy=%t&pin=false&quieter=true`, nocopy), buff)
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())

	if Verbose {
		log.Println("Doing add request...")
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if err != nil {
		return "", err
	}

	hash := new(HashStruct)
	err = dec.Decode(&hash)

	if makedir {
		toSplit := strings.Split(to, "/")
		if Verbose {
			log.Println("Creating parent directory...")
		}
		err = MakeDir(strings.Join(toSplit[:len(toSplit)-1], "/"))
		if err != nil {
			return "", err
		}
	}

	if overwrite {
		if Verbose {
			log.Println("Removing existing file (if any)...")
		}
		RemoveFile(to)
	}

	if Verbose {
		log.Println("Adding file to mfs path:", BasePath+to)
	}
	repl, err := doRequest(fmt.Sprintf(`files/cp?arg=%s&arg=%s`, "/ipfs/"+url.QueryEscape(hash.Hash), url.QueryEscape(BasePath+to)))
	if err != nil || repl != "" {
		//if repl != "" { FIXME check if response is *actually* an error, before deciding it is.
		//	err = errors.New(repl)
		//}
		log.Println(err)
	}
	return hash.Hash, err
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
func ResolveIPNS(key string) (string, error) {
	res, err := doRequest("name/resolve?arg=" + key)
	if err != nil {
		return "", err
	}
	type PathStruct struct {
		Path string
	}
	path := new(PathStruct)
	err = json.Unmarshal([]byte(res), path)
	if err != nil {
		return "", err
	}
	pathSplit := strings.Split(path.Path, "/")
	if len(pathSplit) < 3 {
		return "", errors.New("Unexpected output in name/resolve: " + path.Path)
	}
	return pathSplit[2], nil
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

		// Hash directory if we're using a DB.
		if DB != nil {
			if Verbose {
				log.Println("Hashing", dk.Dir, "...")
			}

			hashmap, err := HashDir(dk.Dir)
			if err != nil {
				log.Panicln("Error hashing directory for hash DB:", err)
			}
			localDirs := make(map[string]bool)
			HashLock.Lock()
			for _, hash := range hashmap {
				if hash.Update() {
					if Verbose {
						log.Println("File updated:", hash.PathOnDisk)
					}

					// grab parent dir, check if we've already created it
					splitName := strings.Split(hash.PathOnDisk, "/")
					parentDir := strings.Join(splitName[:len(splitName)-1], "/")
					makeDir := !localDirs[parentDir]
					if makeDir {
						localDirs[parentDir] = true
					}

					_, err := AddFile(hash.PathOnDisk, dk.MFSPath+"/"+hash.PathOnDisk[len(dk.Dir):], dk.Nocopy, makeDir, false)
					if err != nil {
						log.Println("Error adding file:", err)
					}
				}
				Hashes[hash.PathOnDisk] = hash
			}
			HashLock.Unlock()
		}

		// Check if we recognize any keys, mark them as found, and load them if so.
		for _, ik := range keys.Keys {
			if ik.Name == KeySpace+dk.ID {
				var err error
				dk.CID, err = ResolveIPNS(ik.Id)
				if err != nil {
					log.Println("Error resolving IPNS:", err)
					log.Println("Republishing key...")
					dk.CID = GetFileCID(dk.MFSPath)
					Publish(dk.CID, dk.ID)
				}
				found = true
				log.Println(dk.ID, "loaded:", ik.Id)
				watchDir(dk.Dir, dk.Nocopy)
				break
			}
		}
		if found {
			continue
		}
		log.Println(dk.ID, "not found, generating...")
		ik := GenerateKey(dk.ID)
		var err error
		dk.CID, err = AddDir(dk.Dir, dk.Nocopy)
		if err != nil {
			log.Panicln("[ERROR] Failed to add directory:", err)
		}
		Publish(dk.CID, dk.ID)
		log.Println(dk.ID, "loaded:", ik.Id)
		watchDir(dk.Dir, dk.Nocopy)
	}

	// Main loop
	for {
		time.Sleep(SyncTime)
		for _, dk := range DirKeys {
			if fCID := GetFileCID(dk.MFSPath); len(fCID) > 0 && fCID != dk.CID {
				// log.Printf("[DEBUG] '%s' != '%s'", fCID, dk.CID)
				Publish(fCID, dk.ID)
				UpdatePin(dk.CID, fCID)
				dk.CID = fCID
				log.Println(dk.MFSPath, "updated...")
			}
		}
	}
}

// ConfigFileStruct is used for loading information from the config file.
type ConfigFileStruct struct {
	BasePath     string
	EndPoint     string
	Dirs         []*DirKey
	Sync         string
	Ignore       []string
	DB           string
	IgnoreHidden bool
}

func loadConfig(path string) {
	log.Println("Loading config file", path)
	cfgFile, err := os.Open(path)
	if err != nil {
		log.Println("Config file not found, generating...")
		err = ioutil.WriteFile(path, []byte(DefaultConfig), 0644)
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
		log.Fatalln(`dirs field is required as flag, or in config (ex: {"ID":"UniqueIdentifier", "Dir":"/path/to/dir/to/sync/", "Nocopy": false}).`)
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
	_, err := doRequest("version")
	if err != nil {
		log.Fatalln("Failed to connect to end point:", err)
	}

	// Ignore has no defaults so we need to set them here (if nothing else set it)
	if len(IgnoreFlag.Ignores) > 0 {
		Ignore = IgnoreFlag.Ignores
	} else if len(Ignore) == 0 {
		Ignore = []string{"kate-swp", "swp", "part"}
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
	if *IgnoreHiddenFlag {
		IgnoreHidden = true
	}
	Verbose = *VerboseFlag
}

func main() {
	// Process config and flags.
	ProcessFlags()

	// Start WatchDog.
	WatchDog()
}
