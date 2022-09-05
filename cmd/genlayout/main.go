// Copyright (c) Microsoft Corporation
// Licensed under the MIT License.

// genlayout generates static layout information and
// a decoding method for all the table structs defined
// in the tables.go file of the working directory.
//
// The necessary information is taken using the following rules:
//
// - All structs defined in tables.go will be mapped to a WinMD table
// - The table code is taken from the struct doc line starting with `// @table=$code`
// - Index properties are treated as a table index
// - Index properties must have the comment `@ref=$table`, where $table is the name of the referenced table
// - CodedIndex properties are treated as a table coded index
// - CodedIndex properties must have the comment `@code=$code`, where $code is the name of the code
// - String properties are treated as a String heap index
// - BlobIndex properties are treated as a Blob heap index
// - GUIDIndex properties are treated as a GUID heap index
//
// genlayout will panic if any of the previous rules are not met.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	// parse
	pkg := parsePackage()
	file := findTableFile(pkg)
	tables := parseTables(pkg, file)

	// write
	w := new(bytes.Buffer)
	writePrelude(w)
	writeCodedTable(w, tables)
	writeTableValues(w, tables)
	writeTableWidth(w, tables)
	writeTableImpl(w, tables)
	writeTablesStruct(w, tables)
	writeTableEncoding(w, tables)

	src := formatSource(w.Bytes())
	err := os.WriteFile("zlayout.go", src, 0644)
	if err != nil {
		log.Fatalf("writing output: %s", err)
	}
}

func formatSource(d []byte) []byte {
	src, err := format.Source(d)
	if err != nil {
		// Should never happen, but can arise when developing this code.
		// The user can compile the output to see the error.
		log.Printf("warning: internal error: invalid Go generated: %s", err)
		log.Printf("warning: compile the package to analyze the error")
		err := os.WriteFile("zlayout.go.reject", d, 0644)
		if err != nil {
			log.Fatalf("writing rejected output: %s", err)
		}
	}
	return src
}

func writePrelude(w io.Writer) {
	fmt.Fprintf(w, `
// Copyright (c) Microsoft Corporation
// Licensed under the MIT License.

// Code generated by "genlayout"; DO NOT EDIT.

package winmd

import (
	"fmt"
	"github.com/microsoft/go-winmd/flags"
)

`)
}

func writeTableValues(w io.Writer, tables []tableInfo) {
	// Sort tables by its code so the table
	// values are defined in a nice-looking increasing order.
	sorted := make([]struct {
		name  string
		value uint8
	}, len(tables))
	for i, t := range tables {
		sorted[i].name = t.tableName
		sorted[i].value = t.code
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].value < sorted[j].value
	})

	fmt.Fprintf(w, "// Define table enum\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "type table uint8\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "const (\n")
	for _, t := range sorted {
		fmt.Fprintf(w, "\t%s table = %d\n", t.name, t.value)
	}
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "\ttableMax = %s + 1\n", sorted[len(sorted)-1].name)
	fmt.Fprintf(w, "\ttableNone = tableMax\n")
	fmt.Fprintf(w, ")\n")
}

func writeTableImpl(w io.Writer, tables []tableInfo) {
	fmt.Fprintf(w, "// Implement table interface\n")
	fmt.Fprintf(w, "\n")
	for _, t := range tables {
		fmt.Fprintf(w, "func (%s) table() table { return %s }\n", t.name, t.tableName)
		fmt.Fprintf(w, "\n")
	}
}

func writeTableWidth(w io.Writer, tables []tableInfo) {
	fmt.Fprintf(w, "// Define table width\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "func (t table) width(la *layout) (uint8) {\n")
	fmt.Fprintf(w, "\tswitch t {\n")
	for _, t := range tables {
		fmt.Fprintf(w, "\tcase %s:\n", t.tableName)
		width := make([]string, len(t.fields))
		for i, c := range t.fields {
			switch c.columnType {
			case columnTypeCodedIndex:
				width[i] = "la.codedSizes[coded" + c.coded + "]"
			case columnTypeIndex, columnTypeSlice:
				width[i] = "la.simpleSizes[" + c.tableName + "]"
			case columnTypeString:
				width[i] = "la.stringSize"
			case columnTypeGUID:
				width[i] = "la.guidSize"
			case columnTypeBlob:
				width[i] = "la.blobSize"
			case columnTypeUint:
				width[i] = strconv.Itoa(c.size)
			}
		}
		fmt.Fprintf(w, "\t\treturn %s\n", strings.Join(width, " + "))
	}
	fmt.Fprintf(w, "\tdefault:\n")
	fmt.Fprintf(w, "\t\tpanic(fmt.Sprintf(\"table %%v not supported\", t))\n")
	fmt.Fprintf(w, "\t}\n")
	fmt.Fprintf(w, "}\n")
	fmt.Fprintf(w, "\n")
}

func writeCodedTable(w io.Writer, tables []tableInfo) {
	fmt.Fprintf(w, "// Define CodedTable function\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "// CodedTable returns the table associated to c.\n")
	fmt.Fprintf(w, "func (t *Tables) CodedTable(c CodedIndex) *Table[Record] {\n")
	fmt.Fprintf(w, "\tswitch c.table {\n")
	for _, t := range tables {
		if !t.exported {
			continue
		}
		fmt.Fprintf(w, "\tcase %s:\n", t.tableName)
		fmt.Fprintf(w, "\t\treturn (*Table[Record])(&t.%s)\n", t.name)
	}
	fmt.Fprintf(w, "\tdefault:\n")
	fmt.Fprintf(w, "\t\treturn nil\n")
	fmt.Fprintf(w, "\t}\n")
	fmt.Fprintf(w, "}\n")
}

func writeTablesStruct(w io.Writer, tables []tableInfo) {
	fmt.Fprintf(w, "// Define tables struct\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "type tables struct {\n")
	for _, t := range tables {
		if !t.exported {
			continue
		}
		fmt.Fprintf(w, "\t%s Table[%s]\n", t.name, t.name)
	}
	fmt.Fprintf(w, "}\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "func initTables(t *Tables) {\n")
	for _, t := range tables {
		if !t.exported {
			continue
		}
		fmt.Fprintf(w, "\tt.%s = newTable[%s](t, %s)\n", t.name, t.name, t.tableName)
	}
	fmt.Fprintf(w, "}\n")
}

func writeTableEncoding(w io.Writer, tables []tableInfo) {
	fmt.Fprintf(w, "// Define table decoding functions\n")
	fmt.Fprintf(w, "\n")
	for _, t := range tables {
		fmt.Fprintf(w, "func (rec *%s) decode(r recordReader) error {\n", t.name)
		for _, f := range t.fields {
			switch f.columnType {
			case columnTypeIndex:
				fmt.Fprintf(w, "\trec.%s = r.index(%s)\n", f.name, f.tableName)
			case columnTypeBlob:
				fmt.Fprintf(w, "\trec.%s = r.blob()\n", f.name)
			case columnTypeGUID:
				fmt.Fprintf(w, "\trec.%s = r.guid()\n", f.name)
			case columnTypeString:
				fmt.Fprintf(w, "\trec.%s = r.string()\n", f.name)
			case columnTypeUint:
				var fn string
				switch f.size {
				case 1:
					fn = "uint8"
				case 2:
					fn = "uint16"
				case 4:
					fn = "uint32"
				default:
					log.Fatalf("unsupported uint size %d", f.size)
				}
				if strings.HasPrefix(f.typeName, "flags.") {
					fmt.Fprintf(w, "\trec.%s = %s(r.%s())\n", f.name, f.typeName, fn)
				} else {
					fmt.Fprintf(w, "\trec.%s = r.%s()\n", f.name, fn)
				}
			case columnTypeCodedIndex:
				fmt.Fprintf(w, "\trec.%s = r.coded(coded%s)\n", f.name, f.coded)
			case columnTypeSlice:
				fmt.Fprintf(w, "\trec.%s = r.slice(%s, %s)\n", f.name, t.tableName, f.tableName)
			}
		}
		fmt.Fprintf(w, "\treturn r.err\n")
		fmt.Fprintf(w, "}\n")
		fmt.Fprintf(w, "\n")
	}
}
