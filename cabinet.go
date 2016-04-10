package main

import (
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"path"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/fsnotify/fsnotify"
	"github.com/ipfs/go-ipfs-api"
)

const (
	ipfsNode = "localhost:5001"
	boltDB   = "cabinet.db"
	nodeList = "NODELIST"
)

func Write(loc string, handle string, root string, mhash string) error {
	db, err := bolt.Open(path.Join(root, boltDB), 0600, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(handle))
		if err != nil {
			return err
		}
		return bucket.Put([]byte(loc), []byte(mhash))
	})
	return nil
}

func Read(id string, handle string, root string) (string, error) {
	db, err := bolt.Open(path.Join(root, boltDB), 0600, nil)
	if err != nil {
		return "", err
	}
	var retval string
	defer db.Close()
	db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(handle))
		v := bucket.Get([]byte(id))
		retval = string(v)
		return nil
	})
	return retval, nil
}

func PrintDB(handle string, root string) error {
	db, err := bolt.Open(path.Join(root, boltDB), 0600, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(handle))
		bucket.ForEach(func(k, v []byte) error {
			fmt.Printf("%s => %s\n", k, v)
			return nil
		})
		return nil
	})
	return nil
}

// Add and pin all files in the ~/cabinet directory.
func Upload(handle string, root string) error {
	s := shell.NewShell(ipfsNode)
	return filepath.Walk(root, func(loc string, f os.FileInfo, err error) error {
		stat, err := os.Stat(loc)
		// if stat.Name() == boltDB || stat.IsDir() == true {
		if stat.IsDir() == true {
			return nil
		}
		fi, err := os.Open(loc)
		if err != nil {
			return err
		}
		mhash, err := s.Add(fi)
		if err != nil {
			return err
		}
		Write(loc, handle, root, mhash)
		if err = s.Pin(mhash); err != nil {
			return err
		}
		return nil
	})
}

func UpdateNodeList(handle string, root string) error {

	// get current node's IPFS ID
	s := shell.NewShell(ipfsNode)
	id, err := s.ID()
	if err != nil {
		log.Fatal(err)
	}

	// update stored node list if needed
	nodes, err := Read(nodeList, handle, root)
	if err != nil {
		return err
	}
	if nodes != "" {
		fmt.Printf("Nodes: %s\n", nodes)

		// check if current node is already in node list
		nodeArray := strings.Split(nodes, ",")
		fmt.Println(nodeArray)
		nodeInList := false
		for _, node := range nodeArray {
			if node == id.ID {
				nodeInList = true
				break
			}
		}
		if nodeInList == false {
			nodes = id.ID + "," + nodes
			Write(nodeList, handle, root, nodes)
		}
	} else {
		Write(nodeList, handle, root, id.ID)
	}
	return nil
}

func Watch(handle string, root string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	s := shell.NewShell(ipfsNode)
	id, err := s.ID()
	if err != nil {
		log.Fatal(err)
	}

	done := make(chan bool)
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				log.Println("event:", event)
				if event.Op&fsnotify.Create == fsnotify.Create {
					fi, err := os.Open(event.Name)
					if err != nil {
						log.Println("error:", err)
					}
					newHash, err := s.Add(fi)
					if err != nil {
						log.Println("error:", err)
					}
					Write(event.Name, handle, root, newHash)
					if err = s.Pin(newHash); err != nil {
						log.Println("error:", err)
					}
					PrintDB(handle, root)

					// get cabinet.db file hash
					dbHash, err := Read(path.Join(root, boltDB), handle, root)
					if err != nil {
						log.Fatal(err)
					}
					fmt.Printf("Cabinet DB: %s\n", dbHash)

					// publish dbHash to public key
					fmt.Println("Publishing...")
					if err = s.Publish("", dbHash); err != nil {
						log.Fatal(err)
					}
					ipfshash, err := s.Resolve(id.ID)
					if err != nil {
						log.Fatal(err)
					}
					fmt.Println(ipfshash)
				} else if event.Op&fsnotify.Write == fsnotify.Write {
					if event.Name != path.Join(root, boltDB) {
						curHash, err := Read(event.Name, handle, root)
						if err != nil {
							log.Println("error:", err)
						}
						fi, err := os.Open(event.Name)
						if err != nil {
							log.Println("error:", err)
						}
						newHash, err := s.Add(fi)
						if err != nil {
							log.Println("error:", err)
						}

						// if the file hash has changed, update db
						if newHash != curHash {
							Write(event.Name, handle, root, newHash)
							if err = s.Pin(newHash); err != nil {
								log.Println("error:", err)
							}
							PrintDB(handle, root)

							// get cabinet.db file hash
							dbHash, err := Read(path.Join(root, boltDB), handle, root)
							if err != nil {
								log.Fatal(err)
							}
							fmt.Printf("Cabinet DB: %s\n", dbHash)

							// publish dbHash to public key
							fmt.Println("Publishing...")
							if err = s.Publish("", dbHash); err != nil {
								log.Fatal(err)
							}
							ipfshash, err := s.Resolve(id.ID)
							if err != nil {
								log.Fatal(err)
							}
							fmt.Println(ipfshash)
						}
					}
				}
			case err := <-watcher.Errors:
				log.Println("error:", err)
			}
		}
	}()

	if err = watcher.Add(root); err != nil {
		log.Fatal(err)
	}
	<-done
}

func main() {
	handle := "jack"

	// add and pin ~/cabinet to IPFS
	usr, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}
	root := path.Join(usr.HomeDir, "cabinet")
	if err = Upload(handle, root); err != nil {
		log.Fatal(err)
	}
	UpdateNodeList(handle, root)
	Watch(handle, root)
}
