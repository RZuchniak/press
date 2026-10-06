package language

func optionValue(v value, present bool) value {
	if !present {
		return value{kind: enumValue, object: &objectValue{variant: "None"}}
	}
	return value{kind: enumValue, object: &objectValue{variant: "Some", fields: []value{v}}}
}

// Collection layouts are immutable and shared, like prepared struct layouts.
var listLayout = structLayout{name: "List", collection: listCollection}
var mapLayout = structLayout{name: "Map", collection: mapCollection}

func newCollection(kind collectionKind, values []value) value {
	o := &objectValue{}
	switch kind {
	case listCollection:
		o.layout = &listLayout
		o.fields = append([]value(nil), values...)
	case mapCollection:
		o.layout = &mapLayout
		o.entries = map[value]value{}
	default:
		fail("Invalid collection type")
	}
	return value{kind: structValue, object: o}
}

func collectionCall(method collectionOp, values []value) value {
	o := object(values[0])
	if o.layout == nil || o.layout.collection == noCollection {
		fail("Expected collection")
	}
	if !collectionContracts[method].supports(o.layout.collection) {
		fail("Unsupported collection operation")
	}
	if method == collectionLen {
		n := len(o.fields)
		if o.layout.collection == mapCollection {
			n = len(o.entries)
		}
		return value{kind: intValue, number: int64(n)}
	}
	if o.layout.collection == listCollection {
		switch method {
		case collectionPush:
			o.fields = append(o.fields, values[1])
			return value{}
		case collectionPop:
			if len(o.fields) == 0 {
				return optionValue(value{}, false)
			}
			i := len(o.fields) - 1
			v := o.fields[i]
			o.fields[i] = value{}
			o.fields = o.fields[:i]
			return optionValue(v, true)
		case collectionGet, collectionSet:
			i := values[1].number
			valid := i >= 0 && i < int64(len(o.fields))
			if method == collectionSet {
				if valid {
					o.fields[int(i)] = values[2]
				}
				return boolean(valid)
			}
			if !valid {
				return optionValue(value{}, false)
			}
			return optionValue(copyValue(o.fields[int(i)]), true)
		}
	} else {
		key := values[1]
		v, present := o.entries[key]
		switch method {
		case collectionContains:
			return boolean(present)
		case collectionGet:
			return optionValue(copyValue(v), present)
		case collectionSet:
			o.entries[key] = values[2]
			return optionValue(v, present)
		case collectionRemove:
			delete(o.entries, key)
			return optionValue(v, present)
		}
	}
	fail("Unknown collection operation")
	return value{}
}
