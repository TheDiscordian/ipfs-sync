package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

const (
	KeySpace = "ipfs-sync."
	API      = "/api/v0/"
)

func findInStringSlice(slice []string, val string) int {
	for i, item := range slice {
		if item == val {
			return i
		}
	}
	return -1
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
			filePathSplit := strings.Split(path, string(os.PathSeparator))
			if IgnoreHidden && filePathSplit[len(filePathSplit)-1][0] == '.' {
				return nil
			}
			files = append(files, path)
		} else {
			dirPathSplit := strings.Split(path, string(os.PathSeparator))
			if IgnoreHidden && len(dirPathSplit[len(dirPathSplit)-1]) > 0 && dirPathSplit[len(dirPathSplit)-1][0] == '.' {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return files, err
}

// AddDir adds a directory, and returns CID.
func AddDir(path string, nocopy bool, pin bool, estuary bool) (string, error) {
	pathSplit := strings.Split(path, string(os.PathSeparator))
	dirName := pathSplit[len(pathSplit)-2]
	files, err := filePathWalkDir(path)
	if err != nil {
		return "", err
	}
	localDirs := make(map[string]bool)
	for _, file := range files {
		filePathSplit := strings.Split(file, string(os.PathSeparator))
		if IgnoreHidden && filePathSplit[len(filePathSplit)-1][0] == '.' {
			continue
		}
		splitName := strings.Split(file, ".")
		if findInStringSlice(Ignore, splitName[len(splitName)-1]) > -1 {
			continue
		}
		parentDir := strings.Join(filePathSplit[:len(filePathSplit)-1], string(os.PathSeparator))
		makeDir := !localDirs[parentDir]
		if makeDir {
			localDirs[parentDir] = true
		}
		mfsPath := file[len(path):]
		if os.PathSeparator != '/' {
			mfsPath = strings.ReplaceAll(mfsPath, string(os.PathSeparator), "/")
		}
		_, err := AddFile(file, dirName+"/"+mfsPath, nocopy, makeDir, false)
		if err != nil {
			log.Println("Error adding file:", err)
		}
	}
	cid := GetFileCID(dirName)
	if pin {
		err := Pin(cid)
		log.Println("Error pinning", dirName, ":", err)
	}
	if estuary {
		if err := PinEstuary(cid, dirName); err != nil {
			log.Println("Error pinning to Estuary:", err)
		}
	}
	return cid, err
}

// A simple IPFS add, if onlyhash is true, only the CID is generated and returned
func IPFSAddFile(fpath string, nocopy, onlyhash bool) (*HashStruct, error) {
	f, err := os.Open(fpath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buff := &bytes.Buffer{}
	writer := multipart.NewWriter(buff)

	h := make(textproto.MIMEHeader)
	h.Set("Abspath", fpath)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, "file", url.QueryEscape(f.Name())))
	h.Set("Content-Type", "application/octet-stream")
	part, _ := writer.CreatePart(h)
	if Verbose {
		log.Println("Generating file headers...")
	}
	io.Copy(part, f)

	writer.Close()

	c := &http.Client{}
	req, err := http.NewRequest("POST", EndPoint+API+fmt.Sprintf(`add?nocopy=%t&pin=false&quieter=true&only-hash=%t`, nocopy, onlyhash), buff)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())

	if Verbose {
		log.Println("Doing add request...")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	if err != nil {
		return nil, err
	}

	hash := new(HashStruct)
	err = dec.Decode(&hash)

	if Verbose {
		log.Println("File hash:", hash.Hash)
	}

	return hash, err
}

// AddFile adds a file to the MFS relative to BasePath. from should be the full path to the file intended to be added.
// If makedir is true, it'll create the directory it'll be placed in.
// If overwrite is true, it'll perform an rm before copying to MFS.
func AddFile(from, to string, nocopy bool, makedir bool, overwrite bool) (string, error) {
	log.Println("Adding file from", from, "to", BasePath+to, "...")
	hash, err := IPFSAddFile(from, nocopy, false)
	if err != nil {
		return "", err
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
		}
		if HandleBadBlockError(err, from, nocopy) {
			log.Println("files/cp failure due to filestore, retrying (recursive)")
			AddFile(from, to, nocopy, makedir, overwrite)
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

var fileStoreCleanupLock chan int

func init() {
	fileStoreCleanupLock = make(chan int, 1)
}

// FileStoreEntry is for results returned by `filestore/verify`, only processes Status and Key, as that's all ipfs-sync uses.
type RefResp struct {
	Err string
	Ref string
}

// Completely removes a CID, even if pinned
func RemoveCID(cid string) {
	var found bool
	// Build our own request because we want to stream data...
	c := &http.Client{}
	req, err := http.NewRequest("POST", EndPoint+API+"refs?unique=true&recursive=true&arg="+cid, nil)
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
		found = true
		refResp := new(RefResp)
		err := dec.Decode(refResp)
		if err != nil {
			log.Println("Error decoding ref response stream:", err)
			continue
		}

		newcid := refResp.Ref
		if newcid == "" {
			newcid = cid
		}

		if Verbose {
			log.Println("Removing block:", newcid)
		}
		RemoveBlock(newcid)
	}
	if !found {
		if Verbose {
			log.Println("Removing block:", cid)
		}
		RemoveBlock(cid)
	}
}

// remove block, even if pinned
func RemoveBlock(cid string) {
	var err error
	for _, err = doRequest(TimeoutTime, "block/rm?arg="+cid); err != nil && strings.HasPrefix(err.Error(), "pinned"); _, err = doRequest(TimeoutTime, "block/rm?arg="+cid) {
		splitErr := strings.Split(err.Error(), " ")
		var cid2 string
		if len(splitErr) < 3 { // This is caused by IPFS returning "pinned (recursive)", it means the file in question has been explicitly pinned, and for some unknown reason, it chooses to omit the CID in this particular situation
			cid2 = cid
		} else {
			cid2 = splitErr[2]
		}
		log.Println("Effected block is pinned, removing pin:", cid2)
		_, err := doRequest(0, "pin/rm?arg="+cid2) // no timeout
		if err != nil {
			log.Println("Error removing pin:", err)
		}
	}

	if err != nil {
		log.Println("Error removing bad block:", err)
	}
}

// CleanFilestore removes blocks that point to files that don't exist
func CleanFilestore() {
	select {
	case fileStoreCleanupLock <- 1:
		defer func() { <-fileStoreCleanupLock }()
	default:
		return
	}
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
			RemoveBlock(fsEntry.Key.Slash)
		}
	}
}

// HandleBackBlockError runs CleanFilestore() and returns true if there was a bad block error.
func HandleBadBlockError(err error, fpath string, nocopy bool) bool {
	txt := err.Error()
	if strings.HasPrefix(txt, "failed to get block") || strings.HasSuffix(txt, "no such file or directory") {
		if Verbose {
			log.Println("Handling bad block error: " + txt)
		}
		if fpath == "" { // TODO attempt to get fpath from error msg when possible
			CleanFilestore()
		} else {
			cid, err := IPFSAddFile(fpath, nocopy, true)
			if err == nil {
				RemoveCID(cid.Hash)
			} else {
				log.Println("Error handling bad block error:", err)
			}
		}
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
func UpdatePin(from, to string, nocopy bool) {
	_, err := doRequest(0, "pin/update?arg="+url.QueryEscape(from)+"&arg="+url.QueryEscape(to)) // no timeout
	if err != nil {
		log.Println("Error updating pin:", err)
		if Verbose {
			log.Println("From CID:", from, "To CID:", to)
		}
		if HandleBadBlockError(err, "", nocopy) {
			if Verbose {
				log.Println("Bad blocks found, running pin/update again (recursive)")
			}
			UpdatePin(from, to, nocopy)
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
func ListKeys() (*Keys, error) {
	res, err := doRequest(TimeoutTime, "key/list")
	if err != nil {
		return nil, err
	}
	keys := new(Keys)
	err = json.Unmarshal([]byte(res), keys)
	if err != nil {
		return nil, err
	}
	return keys, nil
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

type EstuaryFile struct {
	Cid  string
	Name string
}

type IPFSRemotePinningResponse struct {
	Count   int
	Results []*IPFSRemotePinResult
}

type IPFSRemotePinResult struct {
	RequestId string
	Pin       *IPFSRemotePin
}

type IPFSRemotePin struct {
	Cid string
}

func doEstuaryRequest(reqType, cmd string, jsonData []byte) (string, error) {
	if EstuaryAPIKey == "" {
		return "", errors.New("Estuary API key is blank.")
	}
	var cancel context.CancelFunc
	ctx := context.Background()
	if TimeoutTime > 0 {
		ctx, cancel = context.WithTimeout(ctx, TimeoutTime)
		defer cancel()
	}
	c := &http.Client{}

	var (
		req *http.Request
		err error
	)
	if jsonData != nil {
		req, err = http.NewRequestWithContext(ctx, reqType, "https://api.estuary.tech/"+cmd, bytes.NewBuffer(jsonData))
	} else {
		req, err = http.NewRequestWithContext(ctx, reqType, "https://api.estuary.tech/"+cmd, nil)
	}
	if err != nil {
		return "", err
	}

	req.Header.Add("Authorization", "Bearer "+EstuaryAPIKey)
	req.Header.Add("Content-Type", "application/json")
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

func PinEstuary(cid, name string) error {
	jsonData, _ := json.Marshal(&EstuaryFile{Cid: cid, Name: name})
	_, err := doEstuaryRequest("POST", "pinning/pins", jsonData)
	return err
}

func UpdatePinEstuary(oldcid, newcid, name string) {
	resp, err := doEstuaryRequest("GET", "pinning/pins?cid="+oldcid, nil)
	if err != nil {
		log.Println("Error getting Estuary pin:", err)
		return
	}
	pinResp := new(IPFSRemotePinningResponse)
	err = json.Unmarshal([]byte(resp), pinResp)
	if err != nil {
		log.Println("Error decoding Estuary pin list:", err)
		return
	}
	// FIXME Estuary doesn't seem to support `cid` GET field yet, this code can be removed when it does:
	var reqId string
	pinResp.Count = 0
	for _, pinResult := range pinResp.Results {
		if pinResult.Pin.Cid == oldcid {
			reqId = pinResult.RequestId
			pinResp.Count = 1
			break
		}
	}
	// END OF FIXME
	jsonData, _ := json.Marshal(&EstuaryFile{Cid: newcid, Name: name})
	if pinResp.Count > 0 {
		_, err := doEstuaryRequest("POST", "pinning/pins/"+reqId, jsonData)
		if err != nil {
			log.Println("Error updating Estuary pin:", err)
		} else {
			return
		}
	}
	err = PinEstuary(newcid, name)
	if err != nil {
		log.Println("Error pinning to Estuary:", err)
	}
}

// WatchDog watches for directory updates, periodically updates IPNS records, and updates recursive pins.
func WatchDog() {
	// Init WatchDog
	keys, err := ListKeys()
	if err != nil {
		log.Fatalln("Failed to retrieve keys:", err)
	}
	for _, dk := range DirKeys {
		found := false

		splitPath := strings.Split(dk.Dir, string(os.PathSeparator))
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
					splitName := strings.Split(hash.PathOnDisk, string(os.PathSeparator))
					parentDir := strings.Join(splitName[:len(splitName)-1], string(os.PathSeparator))
					makeDir := !localDirs[parentDir]
					if makeDir {
						localDirs[parentDir] = true
					}

					mfsPath := hash.PathOnDisk[len(dk.Dir):]
					if os.PathSeparator != '/' {
						mfsPath = strings.ReplaceAll(mfsPath, string(os.PathSeparator), "/")
					}
					_, err := AddFile(hash.PathOnDisk, dk.MFSPath+"/"+mfsPath, dk.Nocopy, makeDir, false)
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
		dk.CID, err = AddDir(dk.Dir, dk.Nocopy, dk.Pin, dk.Estuary)
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
				if dk.Pin {
					UpdatePin(dk.CID, fCID, dk.Nocopy)
				}
				if dk.Estuary {
					UpdatePinEstuary(dk.CID, fCID, strings.Split(dk.MFSPath, "/")[0])
				}
				Publish(fCID, dk.ID)
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
			if VerifyFilestore {
				CleanFilestore()
			}
			break
		}
	}

	// Start WatchDog.
	log.Println("Starting watchdog...")
	WatchDog()
}
