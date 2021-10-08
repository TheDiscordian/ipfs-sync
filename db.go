package main

import (
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/cespare/xxhash/v2"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"sync"
)

var (
	DB *leveldb.DB

	HashLock *sync.RWMutex
	Hashes   map[string]*FileHash
)

type FileHash struct {
	PathOnDisk string
	Hash       []byte
	FakeHash   []byte // timestamp
}

// Update cross-references the hash at PathOnDisk with the one in the db, updating if necessary. Returns true if updated.
func (fh *FileHash) Update() bool {
	if DB == nil || fh == nil {
		return false
	}
	var tsChanged bool
	var hashChanged bool
	if fh.Hash != nil {
		dbhash, err := DB.Get([]byte(fh.PathOnDisk), nil)
		if err != nil || string(dbhash) != string(fh.Hash) {
			DB.Put([]byte(fh.PathOnDisk), fh.Hash, nil)
			hashChanged = true
		}
	} else {
		hashChanged = true
	}
	dbts, err := DB.Get([]byte("ts_"+fh.PathOnDisk), nil)
	if err != nil || string(dbts) != string(fh.FakeHash) {
		DB.Put([]byte("ts_"+fh.PathOnDisk), fh.FakeHash, nil)
		tsChanged = true
	}
	return hashChanged && tsChanged
}

// Delete removes the PathOnFisk:Hash from the db, works with directories. path is used in case fh is nil (directory)
func (fh *FileHash) Delete(path string) {
	if DB == nil {
		return
	}
	if fh != nil {
		path = fh.PathOnDisk
	}
	iter := DB.NewIterator(util.BytesPrefix([]byte(path)), nil)
	for iter.Next() {
		path := iter.Key()
		if Verbose {
			log.Println("Deleting", string(path), "from DB ...")
		}
		DB.Delete(path, nil)
		DB.Delete([]byte("ts_"+string(path)), nil)
		delete(Hashes, string(path))
	}
	iter.Release()
}

// Recalculate simply recalculates the Hash, updating Hash and PathOnDisk, and returning a copy of the pointer.
func (fh *FileHash) Recalculate(PathOnDisk string, dontHash bool) *FileHash {
	fh.PathOnDisk = PathOnDisk
	timestamp := GetHashValue(PathOnDisk, true)
	if string(timestamp) != string(fh.FakeHash) {
		fh.FakeHash = timestamp
		if !dontHash {
			fh.Hash = GetHashValue(PathOnDisk, false)
		}
	}
	return fh
}

func GetHashValue(fpath string, dontHash bool) []byte {
	if !dontHash {
		f, err := os.Open(fpath)
		if err != nil {
			return nil
		}
		hash := xxhash.New()
		if _, err := io.Copy(hash, f); err != nil {
			f.Close()
			return nil
		}
		f.Close()
		return hash.Sum(nil)
	} else {
		fi, err := os.Stat(fpath)
		if err != nil {
			return nil
		}
		size := fi.Size()
		time := fi.ModTime().Unix()
		return []byte{byte(0xff & size), byte(0xff & (size >> 8)), byte(0xff & (size >> 16)), byte(0xff & (size >> 32)),
			byte(0xff & (size >> 40)), byte(0xff & (size >> 48)), byte(0xff & (size >> 56)), byte(0xff & (size >> 64)),
			byte(0xff & time), byte(0xff & (time >> 8)), byte(0xff & (time >> 16)), byte(0xff & (time >> 32)),
			byte(0xff & (time >> 40)), byte(0xff & (time >> 48)), byte(0xff & (time >> 56)), byte(0xff & (time >> 64)),
		}
	}
}

// HashDir recursively searches through a directory, hashing every file, and returning them as a list []*FileHash.
func HashDir(path string, dontHash bool) (map[string]*FileHash, error) {
	files, err := filePathWalkDir(path)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]*FileHash, len(files))
	for _, file := range files {
		if Verbose {
			log.Println("Loading", file, "...")
		}
		splitName := strings.Split(file, ".")
		if findInStringSlice(Ignore, splitName[len(splitName)-1]) > -1 {
			continue
		}

		// Load existing data from DB
		var hash, timestamp []byte
		if !dontHash {
			hash, _ = DB.Get([]byte(file), nil)
		}
		timestamp, _ = DB.Get([]byte("ts_"+file), nil)
		fh := &FileHash{PathOnDisk: file, Hash: hash, FakeHash: timestamp}
		fh.Recalculate(file, dontHash) // Recalculate using info from DB (avoiding rehash if possible)
		hashes[file] = fh
	}
	return hashes, nil
}

// InitDB initializes a database at `path`.
func InitDB(path string) {
	Hashes = make(map[string]*FileHash)
	HashLock = new(sync.RWMutex)
	tdb, err := leveldb.OpenFile(path, nil)
	if err != nil {
		log.Fatalln(err)
	}
	DB = tdb
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	signal.Notify(c, os.Interrupt, syscall.SIGINT)
	go func() {
		<-c
		DB.Close()
		os.Exit(1)
	}()
}
