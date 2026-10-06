package language

// Collection contracts are shared by type checking and bytecode preparation.
// Serialized names are resolved here; execution uses the numeric operations.
type collectionKind uint8

const (
	noCollection collectionKind = iota
	listCollection
	mapCollection
)

func parseCollection(name string) collectionKind {
	switch name {
	case "List":
		return listCollection
	case "Map":
		return mapCollection
	}
	return noCollection
}

type collectionOp uint8

const (
	invalidCollectionOp collectionOp = iota
	collectionLen
	collectionGet
	collectionPush
	collectionPop
	collectionSet
	collectionRemove
	collectionContains
)

type collectionContract struct {
	name             string
	arguments        int
	returns, mutable bool
	list, dict       bool
}

var collectionContracts = [...]collectionContract{
	{},
	{name: "len", returns: true, list: true, dict: true},
	{name: "get", arguments: 1, returns: true, list: true, dict: true},
	{name: "push", arguments: 1, mutable: true, list: true},
	{name: "pop", returns: true, mutable: true, list: true},
	{name: "set", arguments: 2, returns: true, mutable: true, list: true, dict: true},
	{name: "remove", arguments: 1, returns: true, mutable: true, dict: true},
	{name: "contains", arguments: 1, returns: true, dict: true},
}

func resolveCollectionOp(name string) collectionOp {
	for op := collectionLen; int(op) < len(collectionContracts); op++ {
		if collectionContracts[op].name == name {
			return op
		}
	}
	return invalidCollectionOp
}

func (contract collectionContract) supports(kind collectionKind) bool {
	return kind == listCollection && contract.list || kind == mapCollection && contract.dict
}
