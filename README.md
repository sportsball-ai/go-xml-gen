# go-xml-gen

This utility automatically generates `UnmarshalXML` implementations for structs so `encoding/xml` doesn't have to use `reflect`. This results in roughly a 30% decode time speedup.
