package main

import (
	"bytes"
	"context"
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

func findInStringSlice(slice []string, val string) int {
	for i, item := range slice {
		if item == val {
			return i
		}
	}
	return -1
}

func watchDir(dir string, nocopy bool, dontHash bool) chan bool {
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
			if Verbose {
				log.Println("AddFile reply:", repl)
			}
		}
		if Hashes != nil {
			HashLock.Lock()
			if Hashes[fname] != nil {
				Hashes[fname].Recalculate(fname, dontHash)
			} else {
				Hashes[fname] = new(FileHash).Recalculate(fname, dontHash)
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

// doRequest does an API request to the node specified in EndPoint. If timeout is 0 it isn't used.
func doRequest(timeout time.Duration, cmd string) (string, error) {
	var cancel context.CancelFunc
	ctx := context.Background()
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	c := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, "POST", EndPoint+API+cmd, nil)
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

	errStruct := new(ErrorStruct)
	err = json.Unmarshal(body, errStruct)
	if err == nil {
		if errStruct.Error() != "" {
			return string(body), errStruct
		}
	}

	return string(body), nil
}

// HashStruct is useful when you only care about the returned hash.
type HashStruct struct {
	Hash string
}

// GetFileCID gets a file CID based on MFS path relative to BasePath.
func GetFileCID(filePath string) string {
	out, _ := doRequest(TimeoutTime, "files/stat?hash=true&arg="+url.QueryEscape(BasePath+filePath))

	fStat := new(HashStruct)

	err := json.Unmarshal([]byte(out), &fStat)
	if err != nil {
		return ""
	}
	return fStat.Hash
}

// RemoveFile removes a file from the MFS relative to BasePath.
func RemoveFile(fpath string) error {
	_, err := doRequest(TimeoutTime, fmt.Sprintf(`files/rm?arg=%s&force=true`, url.QueryEscape(BasePath+fpath)))
	return err
}

// MakeDir makes a directory along with parents in path
func MakeDir(path string) error {
	_, err := doRequest(TimeoutTime, fmt.Sprintf(`files/mkdir?arg=%s&parents=true`, url.QueryEscape(BasePath+path)))
	return err
}

func filePathWalkDir(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, info fs.DirEntry, err error) error {
		if info == nil {
			return errors.New(fmt.Sprintf("cannot access '%s' for crawling", path))
		}
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
		_, err := AddFile(file, dirName+"/"+file[len(path):], nocopy, makeDir, false)
		if err != nil {
			log.Println("Error adding file:", err)
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

	if Verbose {
		log.Println("File hash:", hash.Hash)
	}

	if makedir {
		toSplit := strings.Split(to, "/")
		parent := strings.Join(toSplit[:len(toSplit)-1], "/")
		if Verbose {
			log.Printf("Creating parent directory '%s' in MFS...\n", parent)
		}
		err = MakeDir(parent)
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

	// send files/cp request
	if Verbose {
		log.Println("Adding file to mfs path:", BasePath+to)
	}
	_, err = doRequest(TimeoutTime, fmt.Sprintf(`files/cp?arg=%s&arg=%s`, "/ipfs/"+url.QueryEscape(hash.Hash), url.QueryEscape(BasePath+to)))
	if err != nil {
		if Verbose {
			log.Println("Error on files/cp:", err)
			log.Println("fpath:", from)
			if HandleBadBlockError(err) {
				log.Println("files/cp failure due to bad filestore, file not added")
			}
		}
	}
	return hash.Hash, err
}

type FileStoreStatus int

const NoFile FileStoreStatus = 11

type FileStoreKey struct {
	Slash string `json:"/"`
}

// FileStoreEntry is for results returned by `filestore/verify`, only processes Status and Key, as that's all ipfs-sync uses.
type FileStoreEntry struct {
	Status FileStoreStatus
	Key    FileStoreKey
}

// CleanFilestore removes blocks that point to files that don't exist
func CleanFilestore() {
	if Verbose {
		log.Println("Removing blocks that point to a file that doesn't exist from filestore...")
	}

	// Build our own request because we want to stream data...
	c := &http.Client{}
	req, err := http.NewRequest("POST", EndPoint+API+"filestore/verify", nil)
	if err != nil {
		log.Println(err)
		return
	}

	// Send request
	resp, err := c.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	if err != nil {
		log.Println(err)
		return
	}

	// Decode the json stream and process it
	for dec.More() {
		fsEntry := new(FileStoreEntry)
		err := dec.Decode(fsEntry)
		if err != nil {
			log.Println("Error decoding fsEntry stream:", err)
			continue
		}
		if fsEntry.Status == NoFile { // if the block points to a file that doesn't exist, remove it.
			log.Println("Removing reference from filestore:", fsEntry.Key.Slash)
			for _, err := doRequest(TimeoutTime, "block/rm?arg="+fsEntry.Key.Slash); err != nil && strings.HasPrefix(err.Error(), "pinned"); _, err = doRequest(TimeoutTime, "block/rm?arg="+fsEntry.Key.Slash) {
				cid := strings.Split(err.Error(), " ")[2]
				log.Println("Effected block is pinned, removing pin:", cid)
				_, err := doRequest(0, "pin/rm?arg="+cid) // no timeout
				if err != nil {
					log.Println("Error removing pin:", err)
				}
			}

			if err != nil {
				log.Println("Error removing bad block:", err)
			}
		}
	}
}

// HandleBackBlockError runs CleanFilestore() and returns true if there was a bad block error.
func HandleBadBlockError(err error) bool {
	txt := err.Error()
	if strings.HasPrefix(txt, "failed to get block") || strings.HasSuffix(txt, "no such file or directory") {
		CleanFilestore()
		return true
	}
	return false
}

// Pin CID
func Pin(cid string) error {
	resp, err := doRequest(0, "pin/add?arg="+url.QueryEscape(cid)) // no timeout
	if resp != "" {
		if Verbose {
			log.Println("Pin response:", resp)
		}
	}
	return err
}

// ErrorStruct allows us to read the errors received by the IPFS daemon.
type ErrorStruct struct {
	Message string // used for error text
	Error2  string `json:"Error"` // also used for error text
	Code    int
	Type    string
}

// Outputs the error text contained in the struct, statistfies error interface.
func (es *ErrorStruct) Error() string {
	switch {
	case es.Message != "":
		return es.Message
	case es.Error2 != "":
		return es.Error2
	}
	return ""
}

// UpdatePin updates a recursive pin to a new CID, unpinning old content.
func UpdatePin(from, to string) {
	_, err := doRequest(0, "pin/update?arg="+url.QueryEscape(from)+"&arg="+url.QueryEscape(to)) // no timeout
	if err != nil {
		log.Println("Error updating pin:", err)
		if Verbose {
			log.Println("From CID:", from, "To CID:", to)
		}
		if HandleBadBlockError(err) {
			if Verbose {
				log.Println("Bad blocks found, running pin/update again (recursive)")
			}
			UpdatePin(from, to)
			return
		}
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
	res, err := doRequest(TimeoutTime, "key/list")
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
	res, err := doRequest(0, "name/resolve?arg="+key) // no timeout
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
	res, err := doRequest(TimeoutTime, "key/gen?arg="+KeySpace+name)
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
	_, err := doRequest(0, fmt.Sprintf("name/publish?arg=%s&key=%s", url.QueryEscape(cid), KeySpace+key)) // no timeout
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

			hashmap, err := HashDir(dk.Dir, dk.DontHash)
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
				watchDir(dk.Dir, dk.Nocopy, dk.DontHash)
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
		watchDir(dk.Dir, dk.Nocopy, dk.DontHash)
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

func main() {
	// Process config and flags.
	ProcessFlags()

	log.Println("Starting up ipfs-sync", version, "...")

	for _, dk := range DirKeys {
		if dk.Nocopy {
			// Cleanup filestore first.
			CleanFilestore()
			break
		}
	}

	// Start WatchDog.
	log.Println("Starting watchdog...")
	WatchDog()
}
