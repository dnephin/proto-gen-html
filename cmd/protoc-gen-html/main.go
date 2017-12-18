package main

import (
	"bytes"
	"io"
	"io/ioutil"
	"log"
	"os"

	"github.com/dnephin/proto-gen-html/tmpl"
	"github.com/golang/protobuf/proto"
	plugin "github.com/golang/protobuf/protoc-gen-go/plugin"
	"github.com/pkg/errors"
)

func main() {
	setupLogging()

	request, err := readRequest()
	if err != nil {
		log.Fatal(err)
	}

	config, err := loadConfig(request)
	if err != nil {
		log.Fatal(err)
	}

	response, err := tmpl.Generate(request, config)
	if err != nil {
		log.Fatal(err)
	}

	if err := writeResponse(response); err != nil {
		log.Fatal(err)
	}
}

// TODO: use logrus
func setupLogging() {
	log.SetFlags(0)
	log.SetPrefix("protoc-gen-html: ")
}

func readRequest() (*plugin.CodeGeneratorRequest, error) {
	data, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read input")
	}

	request := &plugin.CodeGeneratorRequest{}
	err = proto.Unmarshal(data, request)
	return request, errors.Wrapf(err, "failed to parse request")
}

func writeResponse(response *plugin.CodeGeneratorResponse) error {
	data, err := proto.Marshal(response)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal response")
	}
	_, err = io.Copy(os.Stdout, bytes.NewReader(data))
	return errors.Wrapf(err, "failed to write response to stdout")
}
