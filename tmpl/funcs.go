package tmpl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	descriptor "github.com/golang/protobuf/protoc-gen-go/descriptor"
	"gopkg.in/russross/blackfriday.v2"
	"sourcegraph.com/sourcegraph/prototools/util"
)

// stripExt strips the extension off the path and returns it.
func stripExt(s string) string {
	ext := filepath.Ext(s)
	if len(ext) > 0 {
		return s[:len(s)-len(ext)]
	}
	return s
}

// comments takes a string of comments that contain newlines, it merges all
// newlines together except doubles (i.e. blank lines), and then returns
// segments:
//
//   we like to\n
//   keep width\n
//   below 10\n
//   \n
//   but sometimes we go over\n
//   \t   \n
//   crazy, right?\n
//
// And returns it in segments of blank newlines:
//
//   "we like to keep width below 10"
//   "but sometimes we go over"
//   "crazy, right?"
//
func comments(c string) []string {
	var (
		scanner  = bufio.NewScanner(bytes.NewBufferString(c))
		segments []string
		s        []byte
	)
	for scanner.Scan() {
		text := scanner.Text()
		if len(s) > 0 && len(strings.TrimSpace(text)) == 0 {
			// Blank line, we begin a new segment.
			segments = append(segments, string(s))
			s = s[:0]
			continue
		}
		if len(s) > 0 {
			s = append(s, ' ')
		}
		s = append(s, []byte(text)...)
	}
	// Handle the final segment if there is one.
	if len(s) > 0 {
		segments = append(segments, string(s))
	}
	return segments
}

// cacheItem is a single cache item with a value and a location -- effectively
// it is just used for searching.
type cacheItem struct {
	V interface{}
	L *descriptor.SourceCodeInfo_Location
}

// Functions exposed to templates. The user of the package must first preload
// the FuncMap above for these to be called properly (as they are actually
// closures with context).
type tmplFuncs struct {
	protoFileDescriptor *descriptor.FileDescriptorProto
	outputFile, rootDir string
	protoFile           []*descriptor.FileDescriptorProto
	locCache            []cacheItem
}

// funcMap returns the function map for feeding into templates.
func (f *tmplFuncs) funcMap() template.FuncMap {
	return map[string]interface{}{
		"cleanLabel": f.cleanLabel,
		"cleanType":  f.cleanType,
		"fieldType":  f.fieldType,
		"dict":       f.dict,
		"trimExt":    stripExt,
		"comments":   comments,
		"sub":        f.sub,
		"filepath":   f.filepath,
		"urlToType":  f.urlToType,
		"location":   f.location,
		"AllMessages": func(fixNames bool) []*descriptor.DescriptorProto {
			return util.AllMessages(f.protoFileDescriptor, fixNames)
		},
		"AllEnums": func(fixNames bool) []*descriptor.EnumDescriptorProto {
			return util.AllEnums(f.protoFileDescriptor, fixNames)
		},
		"markdown": func(source string) template.HTML {
			output := blackfriday.Run([]byte(source))
			return template.HTML(output)
		},
	}
}

// cleanLabel returns the clean (i.e. human-readable / protobuf-style) version
// of a label.
func (f *tmplFuncs) cleanLabel(l *descriptor.FieldDescriptorProto_Label) string {
	switch int32(*l) {
	case 1:
		return "optional"
	case 2:
		return "required"
	case 3:
		return "repeated"
	default:
		panic("unknown label")
	}
}

// cleanType returns the last part of a types name, i.e. for a fully-qualified
// type ".foo.bar.baz" it would return just "baz".
func (f *tmplFuncs) cleanType(path string) string {
	split := strings.Split(path, ".")
	return split[len(split)-1]
}

// fieldType returns the clean (i.e. human-readable / protobuf-style) version
// of a field type.
func (f *tmplFuncs) fieldType(field *descriptor.FieldDescriptorProto) string {
	if field.TypeName != nil {
		return f.cleanType(*field.TypeName)
	}
	return util.FieldTypeName(field.Type)
}

// dict builds a map of paired items, allowing you to invoke a template with
// multiple parameters.
func (f *tmplFuncs) dict(pairs ...interface{}) (map[string]interface{}, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("expected pairs")
	}
	m := make(map[string]interface{}, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i].(string)] = pairs[i+1]
	}
	return m, nil
}

// sub performs simple x-y subtraction on integers.
func (f *tmplFuncs) sub(x, y int) int { return x - y }

// filepath returns the output filepath (prefixed by the root directory).
func (f *tmplFuncs) filepath() string {
	return path.Join(f.rootDir, f.outputFile)
}

// urlToType returns a URL to the documentation file for the given type. The
// input type path can be either fully-qualified or not, regardless, the URL
// returned will always have a fully-qualified hash.
//
// TODO(slimsag): have the template pass in the relative type instead of nil,
// so that relative symbol paths work.
func (f *tmplFuncs) urlToType(symbolPath string) string {
	if !util.IsFullyQualified(symbolPath) {
		panic("urlToType: not a fully-qualified symbol path")
	}

	// Resolve the package path for the type.
	file := util.NewResolver(f.protoFile).ResolveFile(symbolPath, nil)
	if file == nil {
		return ""
	}
	pkgPath := file.GetName()

	// Remove the package prefix from types, for example:
	//
	//  pkg.html#.pkg.Type.SubType
	//  ->
	//  pkg.html#Type.SubType
	//
	typePath := util.TrimElem(symbolPath, util.CountElem(file.GetPackage()))

	// Prefix the absolute path with the root directory and swap the extension out
	// with the correct one.
	p := stripExt(pkgPath) + path.Ext(f.outputFile)
	p = path.Join(f.rootDir, p)
	return fmt.Sprintf("%s#%s", p, typePath)
}

// resolvePkgPath resolves the named protobuf package, returning its file path.
//
// TODO(slimsag): This function assumes that the package ("package foo;") is
// named identically to its file name ("foo.proto"). Protoc doesn't pass such
// information to us because it hasn't parsed all the files yet -- we will most
// likely have to scan for the package statement in these dependency files
// ourselves.
func (f *tmplFuncs) resolvePkgPath(pkg string) string {
	// Test this proto file itself:
	if stripExt(filepath.Base(*f.protoFileDescriptor.Name)) == pkg {
		return *f.protoFileDescriptor.Name
	}

	// Test each dependency:
	for _, p := range f.protoFileDescriptor.Dependency {
		if stripExt(filepath.Base(p)) == pkg {
			return p
		}
	}
	return ""
}

// location returns the source code info location for the generic AST-like node
// from the descriptor package.
func (f *tmplFuncs) location(x interface{}) *descriptor.SourceCodeInfo_Location {
	// Validate that we got a sane type from the template.
	pkgPath := reflect.Indirect(reflect.ValueOf(x)).Type().PkgPath()
	if pkgPath != "" && pkgPath != "github.com/golang/protobuf/protoc-gen-go/descriptor" &&
		!strings.HasSuffix(pkgPath, "/vendor/github.com/golang/protobuf/protoc-gen-go/descriptor") {

		panic("expected descriptor type; got " + fmt.Sprintf("%q", pkgPath))
	}

	// If the location cache is empty; we build it now.
	if f.locCache == nil {
		for _, loc := range f.protoFileDescriptor.SourceCodeInfo.Location {
			f.locCache = append(f.locCache, cacheItem{
				V: f.walkPath(loc.Path),
				L: loc,
			})
		}
	}
	return f.findCachedItem(x)
}

// findCachedItem finds and returns a cached location for x.
func (f *tmplFuncs) findCachedItem(x interface{}) *descriptor.SourceCodeInfo_Location {
	for _, i := range f.locCache {
		if i.V == x {
			return i.L
		}
	}
	return nil
}

// walkPath walks through the root node (the protoFileDescriptor.protoFileDescriptor file) descending down the path
// until it is resolved, at which point the value is returned.
func (f *tmplFuncs) walkPath(path []int32) interface{} {
	if len(path) == 0 {
		return f.protoFileDescriptor
	}
	var (
		walker func(id int, v interface{}) bool
		found  interface{}
		target = int(path[0])
	)
	path = path[1:]
	walker = func(id int, v interface{}) bool {
		if id != target {
			return true
		}
		if len(path) == 0 {
			found = v
			return false
		}
		target = int(path[0])
		path = path[1:]
		f.protoFields(reflect.ValueOf(v), walker)
		return false
	}
	f.protoFields(reflect.ValueOf(f.protoFileDescriptor), walker)
	return found
}

// protoFields invokes fn with the protobuf tag ID and its in-memory Go value
// given a descriptor node type. It stops invoking fn when it returns false.
func (f *tmplFuncs) protoFields(node reflect.Value, fn func(id int, v interface{}) bool) {
	indirect := reflect.Indirect(node)

	switch indirect.Kind() {
	case reflect.Slice:
		for i := 0; i < indirect.Len(); i++ {
			if !fn(i, indirect.Index(i).Interface()) {
				return
			}
		}

	case reflect.Struct:
		// Iterate each field.
		for i := 0; i < indirect.NumField(); i++ {
			// Parse the protobuf tag for the ID, e.g. the 49 in:
			// "bytes,49,opt,name=foo,def=hello!"
			tag := indirect.Type().Field(i).Tag.Get("protobuf")
			fields := strings.Split(tag, ",")
			if len(fields) < 2 {
				continue // too few fields
			}

			// Parse the tag ID.
			tagID, err := strconv.Atoi(fields[1])
			if err != nil {
				continue
			}
			if !fn(tagID, indirect.Field(i).Interface()) {
				return
			}
		}
	}
}
