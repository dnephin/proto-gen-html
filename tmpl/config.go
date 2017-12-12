package tmpl

// OperationConfig for rendering an html template from proto source
type OperationConfig struct {
	// Template is the path of the template file to use for generating the
	// target.
	Template string

	// Target is the target proto file for generation. It must match one of the
	// input proto files, or else the template will not be executed.
	Target string

	// Output is the output file to write the executed template contents to.
	Output string
}

// Config for the plugin
type Config struct {
	Root       string
	Operations []OperationConfig
}
