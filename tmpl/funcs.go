package tmpl

import (
	"fmt"
	"html/template"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/dnephin/proto-gen-html/util"
	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"gopkg.in/russross/blackfriday.v2"
)

// trimExt strips the extension off the path and returns it.
func trimExt(s string) string {
	ext := filepath.Ext(s)
	if len(ext) > 0 {
		return s[:len(s)-len(ext)]
	}
	return s
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
	outputFile          string
	urlRoot             string
	protoFiles          []*descriptor.FileDescriptorProto
	locCache            []cacheItem
}

func newDefaultTemplateFuncs() template.FuncMap {
	return (&tmplFuncs{}).funcMap()
}

// funcMap returns the function map for feeding into templates.
func (f *tmplFuncs) funcMap() template.FuncMap {
	return map[string]interface{}{
		"labelString":  labelString,
		"typeBaseName": typeBaseName,
		"fieldType":    fieldType,
		"trimExt":      trimExt,
		"typeURL":      f.typeURL,
		"location":     f.location,
		"allMessages":  util.AllMessages,
		"allEnums":     util.AllEnums,
		"markdown": func(source string) template.HTML {
			return template.HTML(blackfriday.Run([]byte(source)))
		},
		"lastProtoFile": func() *descriptor.FileDescriptorProto {
			return f.protoFiles[len(f.protoFiles)-1]
		},
	}
}

// labelString returns the clean (i.e. human-readable / protobuf-style) version
// of a label.
func labelString(l *descriptor.FieldDescriptorProto_Label) string {
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

// typeBaseName returns the last part of a types name, i.e. for a fully-qualified
// type ".foo.bar.baz" it would return just "baz".
func typeBaseName(path string) string {
	split := strings.Split(path, ".")
	return split[len(split)-1]
}

// fieldType returns the clean (i.e. human-readable / protobuf-style) version
// of a field type.
func fieldType(field *descriptor.FieldDescriptorProto) string {
	if field.TypeName != nil {
		return typeBaseName(*field.TypeName)
	}
	return util.FieldTypeName(field.Type)
}

// typeURL returns a URL to the documentation file for the given type. The
// input type path can be either fully-qualified or not, regardless, the URL
// returned will always have a fully-qualified hash.
//
// TODO(slimsag): have the template pass in the relative type instead of nil,
// so that relative symbol paths work.
func (f *tmplFuncs) typeURL(symbolPath string) string {
	_, file := util.NewResolver(f.protoFiles).Resolve(symbolPath, nil)
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
	p := trimExt(pkgPath) + path.Ext(f.outputFile)
	p = path.Join(f.urlRoot, p)
	return fmt.Sprintf("%s#%s", p, typePath)
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
				V: walkPath(loc.Path, f.protoFileDescriptor),
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
func walkPath(path []int32, protoFileDescriptor *descriptor.FileDescriptorProto) interface{} {
	if len(path) == 0 {
		return protoFileDescriptor
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
		protoFields(reflect.ValueOf(v), walker)
		return false
	}
	protoFields(reflect.ValueOf(protoFileDescriptor), walker)
	return found
}

// protoFields invokes fn with the protobuf tag ID and its in-memory Go value
// given a descriptor node type. It stops invoking fn when it returns false.
func protoFields(node reflect.Value, fn func(id int, v interface{}) bool) {
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
