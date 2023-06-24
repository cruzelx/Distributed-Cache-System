package main

type Node struct {
	Previous *Node
	Next     *Node
	Key      string
	Value    string
}

type DLL struct {
	Head *Node
	Tail *Node
}

func NewDLL() *DLL {
	return &DLL{}
}

func (dll *DLL) Prepend(node *Node) {
	if dll.Head == nil {
		dll.Head = node
		dll.Tail = node
		return
	}
	node.Next = dll.Head
	dll.Head.Previous = node
	dll.Head = node
	node.Previous = nil
}

func (dll *DLL) Append(node *Node) {
	if dll.Head == nil {
		dll.Head = node
		dll.Tail = node
		return
	}
	node.Previous = dll.Tail
	node.Next = nil
	dll.Tail.Next = node
	dll.Tail = node
}

func (dll *DLL) Remove(node *Node) {
	if node == nil {
		return
	}

	if node == dll.Head {
		dll.Head = node.Next
	}

	if node == dll.Tail {
		dll.Tail = node.Previous
	}

	if node.Previous != nil {
		node.Previous.Next = node.Next
	}

	if node.Next != nil {
		node.Next.Previous = node.Previous
	}

	node.Previous = nil
	node.Next = nil
}
