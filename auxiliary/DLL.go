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
	sentinal := &Node{}
	return &DLL{
		Head: sentinal,
		Tail: sentinal,
	}
}

func (dll *DLL) Prepend(node *Node) {
	node.Next = dll.Head
	dll.Head.Previous = node
	dll.Head = node
	node.Previous = nil
}

func (dll *DLL) Remove(node *Node) {
	if node == nil {
		return
	}

	if node == dll.Tail {
		dll.Tail = dll.Tail.Previous
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
