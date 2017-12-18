package tmpl

import (
	"bytes"
	"fmt"
	"html/template"
	"path/filepath"

	gateway "github.com/gengo/grpc-gateway/protoc-gen-grpc-gateway/descriptor"
	"github.com/golang/protobuf/proto"
	descriptor "github.com/golang/protobuf/protoc-gen-go/descriptor"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/pkg/errors"
)

type generator struct {
	config   Config
	request  *plugin.CodeGeneratorRequest
	registry *gateway.Registry // TODO: remove, not used
}

// New returns a new generator for the given template.
func Generate(request *plugin.CodeGeneratorRequest, config Config) (*plugin.CodeGeneratorResponse, error) {
	if len(request.FileToGenerate) == 0 {
		return nil, errors.New("no input files")
	}

	registry := gateway.NewRegistry()
	err := registry.Load(request)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load request")
	}

	g := &generator{request: request, registry: registry, config: config}
	return g.Generate(), nil
}

func (g *generator) Generate() *plugin.CodeGeneratorResponse {
	response := &plugin.CodeGeneratorResponse{}

	errs := new(bytes.Buffer)
	for _, opConfig := range g.config.Operations {
		f, err := g.genTarget(opConfig)
		if err != nil {
			errs.WriteString(fmt.Sprintf("%s\n", err))
			continue
		}
		response.File = append(response.File, f)
	}

	if errs.Len() > 0 {
		response.File = nil
		response.Error = proto.String(errs.String())
	}
	return response
}

type templateContext struct {
	*descriptor.FileDescriptorProto
	Request *plugin.CodeGeneratorRequest
}

func (g *generator) genTarget(opConfig OperationConfig) (*plugin.CodeGeneratorResponse_File, error) {
	protoFile := getProtoFileFromTarget(opConfig.Target, g.request)
	if opConfig.Target != "" && protoFile == nil {
		return nil, errors.Errorf("no input proto file for generator target %q", opConfig.Target)
	}

	tmpl, err := g.loadTemplate(opConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to load template %s", opConfig.Template)
	}

	buf := new(bytes.Buffer)
	funcs := &tmplFuncs{
		protoFileDescriptor: protoFile,
		outputFile:          opConfig.Output,
		rootDir:             g.config.Root,
		protoFile:           g.request.GetProtoFile(),
	}
	ctx := templateContext{
		FileDescriptorProto: protoFile,
		Request:             g.request,
	}
	err = tmpl.Funcs(funcs.funcMap()).Execute(buf, ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to render template")
	}

	return &plugin.CodeGeneratorResponse_File{
		Name:    proto.String(opConfig.Output),
		Content: proto.String(buf.String()),
	}, nil
}

func getProtoFileFromTarget(target string, request *plugin.CodeGeneratorRequest) *descriptor.FileDescriptorProto {
	for _, v := range request.GetProtoFile() {
		if target == v.GetName() {
			return v
		}
	}
	return nil
}

func (g *generator) loadTemplate(opConfig OperationConfig) (*template.Template, error) {
	fullPath, err := filepath.Rel(g.config.Root, opConfig.Template)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to make path relative")
	}
	return template.New("main").ParseFiles(fullPath)
}
