package main

import (
	"log"
	"time"

	"github.com/samuel/go-zookeeper/zk"
)

type Zookeeper struct {
	conn *zk.Conn
}

func (z *Zookeeper) Connect(servers []string, time time.Duration) error {
	conn, _, err := zk.Connect(servers, time)
	if err != nil {
		return err
	}
	z.conn = conn
	return nil
}

func (z *Zookeeper) CreateNode(path string, data []byte) (string, error) {
	flag := int32(0)
	acl := zk.WorldACL(zk.PermAll)

	return z.conn.Create(path, data, flag, acl)
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
func (z *Zookeeper) WatchChildren(path string, changeHandler func()) error {
	children, _, childCh, err := z.conn.ChildrenW(path)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-childCh:
				changeHandler()
				log.Printf("Children updated for path %s: %v", path, children)
			}
		}
	}()

	log.Printf("Children for path %s: %v", path, children)

	changeHandler()
	return nil
}

func (z *Zookeeper) Close() {
	z.conn.Close()
}

