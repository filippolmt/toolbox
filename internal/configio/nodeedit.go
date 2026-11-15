package configio

import (
	yaml "gopkg.in/yaml.v3"
)

// mapPairIndex returns the index i of the key node in a mapping's Content
// (its value node sits at i+1), or -1 when parent is not a mapping or the
// key is absent. It is the single scan every mapping primitive in this
// package shares — ChildValue, findOrAppendPair, RemoveMapKey and
// EnsureChildSeq all route their key lookup through it.
func mapPairIndex(parent *yaml.Node, key string) int {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if k := parent.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			return i
		}
	}
	return -1
}

// EnsureChildSeq looks up key on parent and returns its sequence-node value,
// creating an empty sequence when the key is absent or its value is not a
// sequence. Style is reset to block so appending to a flow `[]` placeholder
// renders multi-line entries readably.
func EnsureChildSeq(parent *yaml.Node, key string) *yaml.Node {
	if i := mapPairIndex(parent, key); i >= 0 {
		v := parent.Content[i+1]
		if v.Kind != yaml.SequenceNode {
			*v = yaml.Node{Kind: yaml.SequenceNode}
		}
		v.Style = 0
		return v
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.SequenceNode}
	parent.Content = append(parent.Content, k, v)
	return v
}

// FindSeqEntryByName scans seq for a mapping item whose name: scalar equals
// name, returning its index and node, or (-1, nil) when absent.
func FindSeqEntryByName(seq *yaml.Node, name string) (int, *yaml.Node) {
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if v := ChildValue(item, "name"); v != nil && v.Kind == yaml.ScalarNode && v.Value == name {
			return i, item
		}
	}
	return -1, nil
}

// RemoveMapKey deletes the key/value pair named key from a mapping node,
// reporting whether anything was removed.
func RemoveMapKey(parent *yaml.Node, key string) bool {
	i := mapPairIndex(parent, key)
	if i < 0 {
		return false
	}
	parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
	return true
}
