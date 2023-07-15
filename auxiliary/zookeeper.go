package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/samuel/go-zookeeper/zk"
)

type Zookeeper struct {
	conn *zk.Conn
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
		conn: conn,
	}
}

func connectToZookeeper(servers []string, time time.Duration) (*zk.Conn, error) {
	conn, _, err := zk.Connect(servers, time)
	if err != nil {
		return nil, err
	}

	return conn, err
}

func (z *Zookeeper) CreateAuxiliaryNode() error {

	auxRoot := "/auxiliaries"

	host := os.Getenv("ID") + ":" + os.Getenv("PORT")
	auxPath := auxRoot + "/" + host

	exists, _, err := z.conn.Exists(auxRoot)
	if err != nil {
		return fmt.Errorf("failed to check if root aux znode exists: %v", err)
	}

	if !exists {
		if _, err := z.conn.Create(auxRoot, []byte{}, int32(0), zk.WorldACL(zk.PermAll)); err != nil {
			return fmt.Errorf("failed to create root aux znode: %v", err)
		}
	}

	exists, _, err = z.conn.Exists(auxPath)
	if err != nil {
		return fmt.Errorf("failed to check if aux node exists: %v", err)
	}

	if !exists {
		if _, err := z.conn.Create(auxPath, []byte{}, int32(0), zk.WorldACL(zk.PermAll)); err != nil {
			return fmt.Errorf("failed to create the aux node: %v", err)
		}

	} else {
		log.Println("Aux node already exists")
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
