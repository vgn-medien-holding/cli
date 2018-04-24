package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/exoscale/egoscale"
)

var cmd = flag.String("cmd", "", "CloudStack command name")
var source = flag.String("apis", "", "listApis response in JSON")
var rtype = flag.String("type", "", "Actual type to check against the cmd (need cmd)")

// fieldInfo represents the inner details of a field
type fieldInfo struct {
	Var       *types.Var
	OmitEmpty bool
	Doc       string
}

// command represents a struct within the source code
type command struct {
	name     string
	sync     string
	s        *types.Struct
	position token.Pos
	fields   map[string]fieldInfo
	errors   map[string]error
}

func main() {
	flag.Parse()

	sourceFile, _ := os.Open(*source)
	decoder := json.NewDecoder(sourceFile)
	apis := new(egoscale.ListAPIsResponse)
	if err := decoder.Decode(&apis); err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}

	fset := token.NewFileSet()
	astFiles := make([]*ast.File, 0)
	files, err := filepath.Glob("*.go")
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, err.Error())
			os.Exit(1)
		}
		astFiles = append(astFiles, f)
	}

	info := types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}

	conf := types.Config{
		Importer: importer.For("source", nil),
	}

	_, err = conf.Check("egoscale", fset, astFiles, &info)
	if err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		os.Exit(1)
	}

	commands := make(map[string]*command)

	for id, obj := range info.Defs {
		if obj == nil || !obj.Exported() {
			continue
		}

		typ := obj.Type().Underlying()

		switch typ.(type) {
		case *types.Struct:
			commands[strings.ToLower(obj.Name())] = &command{
				name:     obj.Name(),
				s:        typ.(*types.Struct),
				position: id.Pos(),
			}
		}
	}

	re := regexp.MustCompile(`\bjson:"(?P<name>[^,"]+)(?P<omit>,omitempty)?"`)
	reDoc := regexp.MustCompile(`\bdoc:"(?P<doc>[^"]+)"`)

	for _, a := range apis.API {
		name := strings.ToLower(a.Name)
		params := a.Params

		if strings.ToLower(*cmd) == name && *rtype != "" {
			name = strings.ToLower(*rtype)
			*cmd = name
			params = a.Response
			fmt.Fprintf(os.Stderr, "Checking return type of %sResult, using %q\n", a.Name, *rtype)
		}

		if command, ok := commands[name]; !ok {
			// too much information
			//fmt.Fprintf(os.Stderr, "Unknown command: %q\n", name)
		} else {
			// mapping from name to field
			command.fields = make(map[string]fieldInfo)
			command.errors = make(map[string]error)

			if a.IsAsync {
				command.sync = " (A)"
			}

			for i := 0; i < command.s.NumFields(); i++ {
				f := command.s.Field(i)

				if !f.IsField() || !f.Exported() {
					continue
				}

				tag := command.s.Tag(i)
				match := re.FindStringSubmatch(tag)
				if len(match) == 0 {
					command.errors[f.Name()] = fmt.Errorf("Field error: no json annotation found")
					continue
				}
				name := match[1]
				omitempty := len(match) == 3 && match[2] == ",omitempty"

				doc := ""
				match = reDoc.FindStringSubmatch(tag)
				if len(match) == 2 {
					doc = match[1]
				}

				command.fields[name] = fieldInfo{
					Var:       f,
					OmitEmpty: omitempty,
					Doc:       doc,
				}
			}

			for _, p := range params {
				field, ok := command.fields[p.Name]

				omit := ""
				if !p.Required {
					omit = ",omitempty"
				}

				if !ok {
					doc := ""
					if p.Description != "" {
						doc = fmt.Sprintf(" doc:%q", p.Description)
					}
					command.errors[p.Name] = fmt.Errorf("missing field:\n\t%s %s `json:\"%s%s\"%s`", strings.Title(p.Name), p.Type, p.Name, omit, doc)
					continue
				}
				delete(command.fields, p.Name)

				typename := field.Var.Type().String()

				if field.Doc != p.Description {
					if field.Doc == "" {
						command.errors[p.Name] = fmt.Errorf("missing doc:\n\t\t`doc:%q`", p.Description)
					} else {
						command.errors[p.Name] = fmt.Errorf("wrong doc want %q got %q", p.Description, field.Doc)
					}
				}

				if p.Required == field.OmitEmpty {
					command.errors[p.Name] = fmt.Errorf("wrong omitempty, want `json:\"%s%s\"`", p.Name, omit)
					continue
				}

				expected := ""
				switch p.Type {
				case "short":
					if typename != "int16" {
						expected = "int16"
					}
				case "integer":
					if typename != "int" {
						expected = "int"
					}
				case "long":
					if typename != "int64" {
						expected = "int64"
					}
				case "boolean":
					if typename != "bool" && typename != "*bool" {
						expected = "bool"
					}
				case "string":
				case "uuid":
				case "date":
				case "tzdate":
					if typename != "string" {
						expected = "string"
					}
				case "list":
					if !strings.HasPrefix(typename, "[]") {
						expected = "[]string"
					}
				case "map":
				case "set":
					if !strings.HasPrefix(typename, "[]") {
						expected = "array"
					}
				default:
					command.errors[p.Name] = fmt.Errorf("Unknown type %q <=> %q", p.Type, field.Var.Type().String())
				}

				if expected != "" {
					command.errors[p.Name] = fmt.Errorf("Expected to be a %s, got %q", expected, typename)
				}
			}

			for name := range command.fields {
				command.errors[name] = fmt.Errorf("Extra field found")
			}
		}
	}

	for name, c := range commands {
		pos := fset.Position(c.position)
		er := len(c.errors)

		if *cmd == "" {
			if er != 0 {
				fmt.Printf("%5d %s: %s%s\n", er, pos, c.name, c.sync)
			}
		} else if strings.ToLower(*cmd) == name {
			for k, e := range c.errors {
				fmt.Printf("%s: %s\n", k, e.Error())
			}
			fmt.Printf("\n%s: %s%s has %d error(s)\n", pos, c.name, c.sync, er)
			os.Exit(er)
		}
	}

	if *cmd != "" {
		fmt.Printf("%s not found\n", *cmd)
		os.Exit(1)
	}
}
