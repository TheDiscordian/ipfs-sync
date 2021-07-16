package main

import (
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

func findInStringSlice(slice []string, val string) int {
	for i, item := range slice {
		if item == val {
			return i
		}
	}
	return -1
}

func watchDir(dir string, nocopy bool, dontHash bool) chan bool {
	dirSplit := strings.Split(dir, string(os.PathSeparator))
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
			filePathSplit := strings.Split(path, string(os.PathSeparator))
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
		splitName := strings.Split(fname, string(os.PathSeparator))
		parentDir := strings.Join(splitName[:len(splitName)-1], string(os.PathSeparator))
		makeDir := !localDirs[parentDir]
		if makeDir {
			localDirs[parentDir] = true
		}
		mfsPath := fname[len(dir):]
		if os.PathSeparator != '/' {
			mfsPath = strings.ReplaceAll(mfsPath, string(os.PathSeparator), "/")
		}
		repl, err := AddFile(fname, dirName+"/"+mfsPath, nocopy, makeDir, overwrite)
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
			filePathSplit := strings.Split(path, string(os.PathSeparator))
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
				filePathSplit := strings.Split(event.Name, string(os.PathSeparator))
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
					fpath := event.Name[len(dir):]
					if string(os.PathSeparator) != "/" {
						fpath = strings.ReplaceAll(fpath, string(os.PathSeparator), "/")
					}
					log.Println("Removing", dirName+"/"+fpath, "...")
					err = RemoveFile(dirName + "/" + fpath)
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
