package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/types"
	"io"
	"os"
	"strconv"

	"github.com/fatih/structtag"
	"golang.org/x/tools/go/packages"
)

type xmlStructAttributeInfo struct {
	GoName  string
	XMLName string
	Type    types.Type
}

type xmlStructInfo struct {
	Name       string
	IsLeaf     bool
	Attributes []xmlStructAttributeInfo
}

type generateState struct {
	structs              []xmlStructInfo
	existingUnmarshalers map[string]struct{}
}

func (s *generateState) processFile(pkg *packages.Package, f *ast.File) []error {
	var errs []error

	ast.Inspect(f, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncDecl:
			if node.Recv != nil && (node.Name.Name == "UnmarshalXML" || node.Name.Name == "UnmarshalXMLAttr") {
				switch typ := node.Recv.List[0].Type.(type) {
				case *ast.StarExpr:
					if ident, ok := typ.X.(*ast.Ident); ok {
						s.existingUnmarshalers[ident.Name] = struct{}{}
					}
				case *ast.Ident:
					s.existingUnmarshalers[typ.Name] = struct{}{}
				}
			}
		case *ast.TypeSpec:
			switch typ := node.Type.(type) {
			case *ast.StructType:
				info := xmlStructInfo{
					Name:   node.Name.Name,
					IsLeaf: true,
				}

				for _, field := range typ.Fields.List {
					if len(field.Names) != 1 {
						return false
					}

					fieldType := pkg.TypesInfo.Types[field.Type]
					isAttribute := false

					if field.Tag != nil {
						tags, err := structtag.Parse(field.Tag.Value[1 : len(field.Tag.Value)-1])
						if err == nil {
							if xmlTag, err := tags.Get("xml"); xmlTag != nil && err == nil {
								attrInfo := xmlStructAttributeInfo{
									GoName:  field.Names[0].Name,
									XMLName: field.Names[0].Name,
									Type:    fieldType.Type,
								}

								if xmlTag.Name == "-" {
									continue
								} else if xmlTag.Name != "" {
									attrInfo.XMLName = xmlTag.Name
								}

								for _, opt := range xmlTag.Options {
									if opt == "attr" {
										isAttribute = true
									}
								}

								if isAttribute {
									info.Attributes = append(info.Attributes, attrInfo)
								}
							}
						}
					}

					if !isAttribute {
						info.IsLeaf = false
					}
				}
				s.structs = append(s.structs, info)
			default:
			}
			return false
		default:
		}
		return true
	})

	return errs
}

var unmarshalXMLAttr *types.Interface

func init() {
	cfg := &packages.Config{
		Mode: packages.NeedImports | packages.NeedExportsFile | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
	}
	pkgs, err := packages.Load(cfg, "encoding/xml")
	if err != nil {
		panic(err)
	}
	for _, pkg := range pkgs {
		for ident, def := range pkg.TypesInfo.Defs {
			if ident.Name == "UnmarshalerAttr" {
				unmarshalXMLAttr = def.Type().Underlying().(*types.Interface)
				break
			}
		}
	}
}

func generateAttributeAssignment(goName string, typ types.Type, errs *[]error, imports map[string]struct{}, castTypeName string) string {
	output := ""

	methodSet := types.NewMethodSet(typ)
	for i := 0; i < methodSet.Len(); i++ {
		if method := methodSet.At(i); method.Kind() == types.MethodVal {
			if method.Obj().(*types.Func).Name() == "UnmarshalText" {
				output += "if err := v." + goName + ".UnmarshalText([]byte(attr.Value)); err != nil {\n"
				output += `return fmt.Errorf("malformed attribute value for ` + goName + `: %w", err)`
				output += "}\n"
				return output
			} else if method.Obj().(*types.Func).Name() == "UnmarshalXMLAttr" {
				output += "if err := v." + goName + ".UnmarshalXMLAttr(attr); err != nil {\n"
				output += `return fmt.Errorf("malformed attribute value for ` + goName + `: %w", err)`
				output += "}\n"
				return output
			}
		}
	}

	methodSet = types.NewMethodSet(types.NewPointer(typ))
	for i := 0; i < methodSet.Len(); i++ {
		if method := methodSet.At(i); method.Kind() == types.MethodVal {
			if method.Obj().(*types.Func).Name() == "UnmarshalText" {
				output += "if err := v." + goName + ".UnmarshalText([]byte(attr.Value)); err != nil {\n"
				output += `return fmt.Errorf("malformed attribute value for ` + goName + `: %w", err)`
				output += "}\n"
				return output
			} else if method.Obj().(*types.Func).Name() == "UnmarshalXMLAttr" {
				output += "if err := (&v." + goName + ").UnmarshalXMLAttr(attr); err != nil {\n"
				output += `return fmt.Errorf("malformed attribute value for ` + goName + `: %w", err)`
				output += "}\n"
				return output
			}
		}
	}

	assignment := func(v string) string {
		if castTypeName != "" {
			return "v." + goName + " = " + castTypeName + "(" + v + ")\n"
		} else {
			return "v." + goName + " = " + v + "\n"
		}
	}

	switch typ := typ.(type) {
	case *types.Basic:
		switch typ.Kind() {
		case types.Float64:
			imports["fmt"] = struct{}{}
			imports["strconv"] = struct{}{}
			output += `if attr.Value == "" {
	v.` + goName + ` = 0.0
} else if f, err := strconv.ParseFloat(attr.Value, 64); err != nil {
	return fmt.Errorf("malformed attribute value for ` + goName + `: %w", err)
} else {
	` + assignment("f") + `
}
`
		case types.Bool:
			imports["fmt"] = struct{}{}
			imports["strconv"] = struct{}{}
			output += `if attr.Value == "" {
	v.` + goName + ` = false
} else if b, err := strconv.ParseBool(attr.Value); err != nil {
	return fmt.Errorf("malformed attribute value for ` + goName + `: %w", err)
} else {
	` + assignment("b") + `
}
`
		case types.String:
			output += assignment("attr.Value")
		case types.Int:
			imports["fmt"] = struct{}{}
			imports["strconv"] = struct{}{}
			output += `if attr.Value == "" {
	v.` + goName + ` = 0
} else if n, err := strconv.Atoi(attr.Value); err != nil {
	return fmt.Errorf("malformed attribute value for ` + goName + `: %w", err)
} else {
	` + assignment("n") + `
}
`
		default:
			*errs = append(*errs, fmt.Errorf("unsupported attribute type: %s", typ.String()))
		}
	case *types.Named:
		return generateAttributeAssignment(goName, typ.Underlying(), errs, imports, typ.Obj().Name())
	default:
		*errs = append(*errs, fmt.Errorf("unsupported attribute type: %s", typ.String()))
	}
	return output
}

func Generate(pkg *packages.Package) (string, []error) {
	state := &generateState{
		existingUnmarshalers: map[string]struct{}{},
	}

	var errs []error
	for name, f := range pkg.Syntax {
		for _, err := range state.processFile(pkg, f) {
			errs = append(errs, fmt.Errorf("%v: %w", name, err))
		}
	}

	if len(errs) > 0 {
		return "", errs
	}

	imports := map[string]struct{}{}
	output := ""

	for _, info := range state.structs {
		if _, ok := state.existingUnmarshalers[info.Name]; ok || !info.IsLeaf {
			continue
		}
		imports["encoding/xml"] = struct{}{}
		output += "func (v *" + info.Name + ") UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {\n"
		if len(info.Attributes) > 0 {
			output += "for _, attr := range start.Attr {\n"
			output += "switch attr.Name.Local {\n"
			for _, attr := range info.Attributes {
				output += "case " + strconv.Quote(attr.XMLName) + ":\n"
				output += generateAttributeAssignment(attr.GoName, attr.Type, &errs, imports, "")
			}
			output += "}\n"
			output += "}\n"
		}
		output += "return d.Skip()\n}\n\n"
	}

	if len(errs) > 0 {
		return "", errs
	}

	pkgName := "main"
	for _, f := range pkg.Syntax {
		pkgName = f.Name.Name
	}

	tmp := output
	output = "package " + pkgName + "\n\n"
	if len(imports) > 0 {
		output += "import (\n"
		for imp := range imports {
			output += "\"" + imp + "\"\n"
		}
		output += ")\n\n"
	}
	output += tmp

	out, err := format.Source([]byte(output))
	if err != nil {
		fmt.Printf(output)
		return "", []error{fmt.Errorf("error formatting result: %w", err)}
	}
	return string(out), nil
}

func Run(defaultOutput io.Writer, args ...string) []error {
	var outputPath = flag.String("o", "", "the output file")
	flag.Parse()

	if len(flag.Args()) != 1 {
		flag.Usage()
		os.Exit(1)
	}

	if *outputPath != "" {
		if err := os.Remove(*outputPath); err != nil && !os.IsNotExist(err) {
			return []error{fmt.Errorf("error removing existing output: %w", err)}
		}
	}

	cfg := &packages.Config{
		Mode: packages.NeedImports | packages.NeedExportsFile | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
	}
	pkgs, err := packages.Load(cfg, flag.Args()[0])
	if err != nil {
		return []error{err}
	}

	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			errs := make([]error, len(pkg.Errors))
			for i, e := range pkg.Errors {
				errs[i] = e
			}
			return errs
		}
		output, errs := Generate(pkg)
		if len(errs) > 0 {
			return errs
		}

		if *outputPath != "" {
			if err := os.WriteFile(*outputPath, []byte(output), 0644); err != nil {
				return []error{fmt.Errorf("error writing output: %w", err)}
			}
		} else {
			fmt.Fprint(defaultOutput, output)
		}
		return nil
	}

	return []error{fmt.Errorf("no go packages found")}
}

func main() {
	if errs := Run(os.Stdout, os.Args[1:]...); len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		os.Exit(1)
	}
}
