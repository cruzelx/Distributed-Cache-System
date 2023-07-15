package main

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/samuel/go-zookeeper/zk"
)

type Zookeeper struct {
	conn                    *zk.Conn
	removeAuxParentZnode    string
	removeAuxEphemeralZnode string
}

func NewManager() *Zookeeper {
	servers := os.Getenv("ZOO_SERVERS")
	zooServers := strings.Split(servers, ",")

	time.Sleep(time.Second * 10)

	conn, err := connectToZookeeper(zooServers, time.Second*5)
	if err != nil {
		log.Fatalln("Failed to connect to zookeeper server(s): ", err)
	}
	return &Zookeeper{
		conn:                 conn,
		removeAuxParentZnode: "/remove-auxes",
	}
}

func connectToZookeeper(servers []string, time time.Duration) (*zk.Conn, error) {
	log.Printf("zookeeper servers: %s\n", servers)

	conn, _, err := zk.Connect(servers, time)
	if err != nil {
		return nil, err
	}

	return conn, err
}

func (z *Zookeeper) CreateMasterNode() error {

	// get hostname
	host, err := os.Hostname()
	if err != nil {
		log.Fatalln(err)
	}

	masterPath := "/masters/master-" + host

	exists, _, err := z.conn.Exists("/masters")
	if err != nil {
		return fmt.Errorf("failed to check if parent master znode exists: %v", err)
	}

	if !exists {
		if _, err := z.conn.Create("/masters", []byte{}, 0, zk.WorldACL(zk.PermAll)); err != nil {
			return fmt.Errorf("failed to create parent master znode: %v", err)
		}
	}

	exists, _, err = z.conn.Exists(masterPath)
	if err != nil {
		return fmt.Errorf("failed to check if master znode exists: %v", err)
	}

	if !exists {
		flag := int32(0)
		acl := zk.WorldACL(zk.PermAll)

		if _, err := z.conn.Create(masterPath, []byte{}, flag, acl); err != nil {
			return fmt.Errorf("failed to create the master znode: %v", err)
		}

	} else {
		log.Printf("Master znode already exists: %s\n", masterPath)
	}

	return nil
}

func (z *Zookeeper) GetData(path string) ([]byte, error) {
	data, _, err := z.conn.Get(path)
	return data, err
}

func (z *Zookeeper) SetData(path string, data []byte) error {
	_, err := z.conn.Set(path, data, -1)
	return err
}

func (z *Zookeeper) DeleteNode(path string) error {
	return z.conn.Delete(path, -1)
}

// TODO: Need to work on this
func (z *Zookeeper) WatchChildren(path string, changeHandler func(children []string)) error {
	children, _, childCh, err := z.conn.ChildrenW(path)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-childCh:
				changeHandler(children)
				log.Printf("Children updated for path %s: %v", path, children)
			}
		}
	}()

	log.Printf("Children for path %s: %v", path, children)

	changeHandler(children)
	return nil
}

func (z *Zookeeper) Close() {
	z.conn.Close()
}

func (z *Zookeeper) InitRemoveAuxEphemeralZnode() error {
	lockPath := z.removeAuxParentZnode

	exists, _, err := z.conn.Exists(lockPath)
	if err != nil {
		return fmt.Errorf("failed to check if master lock path exists: %v", err)
	}

	if !exists {
		_, err := z.conn.Create(lockPath, []byte{}, int32(0), zk.WorldACL(zk.PermAll))
		if err != nil {
			return fmt.Errorf("failed to create lock path %s: %v", lockPath, err)
		}
	}

	ephimeralLockPath, err := z.conn.Create(lockPath+"/lock-", []byte{}, zk.FlagEphemeral|zk.FlagSequence, zk.WorldACL(zk.PermAll))
	if err != nil {
		return fmt.Errorf("failed to create sequential lock node: %v", err)
	}

	z.removeAuxEphemeralZnode = ephimeralLockPath
	return nil

}

func (z *Zookeeper) WatchOverAuxServers(handler func(auxes []string), stop <-chan zk.Event) {
	auxRoot := "/auxiliaries"
	for {
		_, _, childCh, err := z.conn.ChildrenW(auxRoot)
		if err != nil {
			log.Printf("failed to watch over children of aux servers root znode %s: %v", auxRoot, err)
			return
		}

		select {
		case event := <-childCh:
			if event.Type == zk.EventNodeChildrenChanged {
				auxes, _, err := z.conn.Children(auxRoot)
				if err != nil {
					log.Printf("failed to get children of aux servers root znode %s: %v", auxRoot, err)
				}

				if len(auxes) != 0 {
					handler(auxes)
				}
			}
		case <-stop:
			log.Printf("Stopping watch over data of aux servers root znode %s", auxRoot)
			return
		}

	}
}

func (z *Zookeeper) WatchRemoveAuxEphemeralData(handler func(node string), stop <-chan zk.Event) {

	for {
		_, _, childCh, err := z.conn.GetW(z.removeAuxEphemeralZnode)
		if err != nil {
			log.Printf("failed to get data from znode %s: %v", z.removeAuxEphemeralZnode, err)
			return
		}

		select {

		case event := <-childCh:
			if event.Type == zk.EventNodeDataChanged {

				data, _, err := z.conn.Get(z.removeAuxEphemeralZnode)
				log.Printf("Updated znode data: %s", string(data))

				if err != nil {
					log.Printf("failed to get data from znode %s: %v", z.removeAuxEphemeralZnode, err)
				}

				if string(data) != "" {
					handler(string(data))
				}

				log.Printf("data changed on ephemeral znode %s", z.removeAuxEphemeralZnode)
			}

		// node := string(data)
		// if node == "" {
		// 	log.Printf("Empty znode data: %s", string(data))
		// 	continue
		// } else {
		// 	handler(node)
		// 	_, err := z.conn.Set(z.removeAuxEphemeralZnode, []byte{}, -1)
		// 	if err != nil {
		// 		log.Printf("failed to set data to znode %s: %v", z.removeAuxEphemeralZnode, err)
		// 	}
		// }
		case <-stop:
			log.Printf("Stopping watch over data of znode %s", z.removeAuxEphemeralZnode)
			return
		}

	}

}

func (z *Zookeeper) BroadcastRemoveAuxData(auxServer string) error {
	children, _, err := z.conn.Children(z.removeAuxParentZnode)
	if err != nil {
		return fmt.Errorf("failed to get children of the path %s: %v", z.removeAuxEphemeralZnode, err)
	}

	if len(children) == 0 {
		return nil
	}

	log.Printf("Children of parent znode %s: %v", z.removeAuxParentZnode, children)

	splitted := strings.Split(z.removeAuxEphemeralZnode, "/")
	thisEphemeralNode := splitted[len(splitted)-1]

	for i, ephemeralZnode := range children {
		if thisEphemeralNode == ephemeralZnode {
			continue
		}
		path := z.removeAuxParentZnode + "/" + ephemeralZnode
		log.Printf("ephemeral znode %d: %s", i, path)

		if _, err := z.conn.Set(path, []byte(auxServer), -1); err != nil {
			log.Printf("failed to set data to znode %s: %v", path, err)
		}
	}
	return nil
}
func (z *Zookeeper) LockAndRelease(callback func(value string), param string) error {
	lockPath := "/remove-auxes"

	exists, _, err := z.conn.Exists(lockPath)
	if err != nil {
		return fmt.Errorf("failed to check if master lock path exists: %v", err)
	}

	if !exists {
		_, err := z.conn.Create(lockPath, []byte{}, 0, zk.WorldACL(zk.PermAll))
		if err != nil {
			return fmt.Errorf("failed to create lock path %s: %v", lockPath, err)
		}
	}

	// Acquire lock
	lockNode, err := z.conn.Create(lockPath+"/lock-", []byte{}, zk.FlagEphemeral|zk.FlagSequence, zk.WorldACL(zk.PermAll))
	if err != nil {
		return fmt.Errorf("failed to create sequential lock node: %v", err)
	}

	for {
		children, _, err := z.conn.Children(lockPath)
		if err != nil {
			return fmt.Errorf("failed to get children of the path %s: %v", lockPath, err)
		}

		log.Printf("master lock children: %v", children)

		sort.Strings(children)
		if lockNode == lockPath+"/"+children[0] {
			// call remove aux node
			callback(param)
			log.Printf("Removed aux %s from master", param)
			break
		}

		lockNodeIndex := sort.SearchStrings(children, lockNode[len(lockPath)+1:])
		waitNode := children[lockNodeIndex-1]
		_, _, waitChan, err := z.conn.GetW(lockPath + "/" + waitNode)
		if err != nil {
			return fmt.Errorf("failed to watch over %s: %v", lockPath+"/"+waitNode, err)
		}
		<-waitChan

	}

	err = z.conn.Delete(lockNode, -1)
	if err != nil {
		return fmt.Errorf("failed to delete lock node %s: %v", lockNode, err)
	}

	return nil

}
