// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/hashicorp/terraform-plugin-docs/internal/schemamd"

	"github.com/hashicorp/terraform-plugin-docs/internal/functionmd"
	"github.com/hashicorp/terraform-plugin-docs/internal/mdplain"
	"github.com/hashicorp/terraform-plugin-docs/internal/tmplfuncs"
)

const (
	schemaComment    = "<!-- schema generated by tfplugindocs -->"
	signatureComment = "<!-- signature generated by tfplugindocs -->"
	argumentComment  = "<!-- arguments generated by tfplugindocs -->"
	variadicComment  = "<!-- variadic argument generated by tfplugindocs -->"

	frontmatterComment = "# generated by https://github.com/hashicorp/terraform-plugin-docs"
)

type (
	resourceTemplate string
	functionTemplate string
	providerTemplate string

	docTemplate string
)

func newTemplate(providerDir, name, text string) (*template.Template, error) {
	tmpl := template.New(name)
	titleCaser := cases.Title(language.Und)

	tmpl.Funcs(map[string]interface{}{
		"codefile":      codeFile(providerDir),
		"lower":         strings.ToLower,
		"plainmarkdown": mdplain.PlainMarkdown,
		"prefixlines":   tmplfuncs.PrefixLines,
		"split":         strings.Split,
		"tffile":        terraformCodeFile(providerDir),
		"title":         titleCaser.String,
		"trimspace":     strings.TrimSpace,
		"upper":         strings.ToUpper,
	})

	var err error
	tmpl, err = tmpl.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("unable to parse template %q: %w", text, err)
	}

	return tmpl, nil
}

func codeFile(providerDir string) func(string, string) (string, error) {
	return func(format string, file string) (string, error) {
		if filepath.IsAbs(file) {
			return tmplfuncs.CodeFile(format, file)
		}

		return tmplfuncs.CodeFile(format, filepath.Join(providerDir, file))
	}
}

func terraformCodeFile(providerDir string) func(string) (string, error) {
	// TODO: omit comment handling
	return func(file string) (string, error) {
		if filepath.IsAbs(file) {
			return tmplfuncs.CodeFile("terraform", file)
		}

		return tmplfuncs.CodeFile("terraform", filepath.Join(providerDir, file))
	}
}

func loadMetadata(metadataFile string) (map[string]string, error) {
	if !fileExists(metadataFile) {
		return map[string]string{}, nil
	}
	content, err := os.ReadFile(metadataFile)
	if err != nil {
		return nil, fmt.Errorf("unable to read content from metadata file %q: %w", metadataFile, err)
	}

	var metadata map[string]string

	if err := json.Unmarshal(content, &metadata); err != nil {
		log.Fatalf("failed to unmarshal: %v", err)
	}
	return metadata, nil
}

func renderTemplate(providerDir, name string, text string, out io.Writer, data interface{}) error {
	tmpl, err := newTemplate(providerDir, name, text)
	if err != nil {
		return err
	}

	err = tmpl.Execute(out, data)
	if err != nil {
		return fmt.Errorf("unable to execute template: %w", err)
	}

	return nil
}

func renderStringTemplate(providerDir, name, text string, data interface{}) (string, error) {
	var buf bytes.Buffer

	err := renderTemplate(providerDir, name, text, &buf, data)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (t docTemplate) Render(providerDir string, out io.Writer) error {
	s := string(t)
	if s == "" {
		return nil
	}

	return renderTemplate(providerDir, "docTemplate", s, out, nil)
}

func (t providerTemplate) Render(providerDir, providerName, renderedProviderName, exampleFile string, schema *tfjson.Schema) (string, error) {
	schemaBuffer := bytes.NewBuffer(nil)
	err := schemamd.Render(schema, schemaBuffer)
	if err != nil {
		return "", fmt.Errorf("unable to render schema: %w", err)
	}

	s := string(t)
	if s == "" {
		return "", nil
	}

	return renderStringTemplate(providerDir, "providerTemplate", s, struct {
		Description string

		HasExample  bool
		ExampleFile string

		ProviderName      string
		ProviderShortName string
		SchemaMarkdown    string

		RenderedProviderName string
	}{
		Description: schema.Block.Description,

		HasExample:  exampleFile != "" && fileExists(exampleFile),
		ExampleFile: exampleFile,

		ProviderName:      providerName,
		ProviderShortName: providerShortName(providerName),

		SchemaMarkdown: schemaComment + "\n" + schemaBuffer.String(),

		RenderedProviderName: renderedProviderName,
	})
}

func (t resourceTemplate) Render(providerDir, name, providerName, renderedProviderName, typeName, exampleFile, importFile, metadataFile string, schema *tfjson.Schema) (string, error) {
	schemaBuffer := bytes.NewBuffer(nil)
	err := schemamd.Render(schema, schemaBuffer)
	if err != nil {
		return "", fmt.Errorf("unable to render schema: %w", err)
	}

	s := string(t)
	if s == "" {
		return "", nil
	}

	metadata, err := loadMetadata(metadataFile)
	if err != nil {
		return "", fmt.Errorf("unable to load metadata: %w", err)
	}

	return renderStringTemplate(providerDir, "resourceTemplate", s, struct {
		Type        string
		Name        string
		Description string

		HasExample  bool
		ExampleFile string

		HasImport  bool
		ImportFile string

		ProviderName      string
		ProviderShortName string

		SchemaMarkdown string

		RenderedProviderName string

		HasMetadata  bool
		MetadataFile string
		Metadata     map[string]string
	}{
		Type:        typeName,
		Name:        name,
		Description: schema.Block.Description,

		HasExample:  exampleFile != "" && fileExists(exampleFile),
		ExampleFile: exampleFile,

		HasImport:  importFile != "" && fileExists(importFile),
		ImportFile: importFile,

		ProviderName:      providerName,
		ProviderShortName: providerShortName(providerName),

		SchemaMarkdown: schemaComment + "\n" + schemaBuffer.String(),

		RenderedProviderName: renderedProviderName,

		HasMetadata:  metadataFile != "" && fileExists(metadataFile),
		MetadataFile: metadataFile,
		Metadata:     metadata,
	})
}

func (t functionTemplate) Render(providerDir, name, providerName, renderedProviderName, typeName, exampleFile, metadataFile string, signature *tfjson.FunctionSignature) (string, error) {
	funcSig, err := functionmd.RenderSignature(name, signature)
	if err != nil {
		return "", fmt.Errorf("unable to render function signature: %w", err)
	}

	funcArgs, err := functionmd.RenderArguments(signature)
	if err != nil {
		return "", fmt.Errorf("unable to render function arguments: %w", err)
	}

	funcVarArg, err := functionmd.RenderVariadicArg(signature)
	if err != nil {
		return "", fmt.Errorf("unable to render variadic argument: %w", err)
	}

	s := string(t)
	if s == "" {
		return "", nil
	}

	metadata, err := loadMetadata(metadataFile)
	if err != nil {
		return "", fmt.Errorf("unable to load metadata: %w", err)
	}

	return renderStringTemplate(providerDir, "resourceTemplate", s, struct {
		Type        string
		Name        string
		Description string
		Summary     string

		HasExample  bool
		ExampleFile string

		ProviderName      string
		ProviderShortName string

		FunctionSignatureMarkdown string
		FunctionArgumentsMarkdown string

		HasVariadic                      bool
		FunctionVariadicArgumentMarkdown string

		RenderedProviderName string

		HasMetadata  bool
		MetadataFile string
		Metadata     map[string]string
	}{
		Type:        typeName,
		Name:        name,
		Description: signature.Description,
		Summary:     signature.Summary,

		HasExample:  exampleFile != "" && fileExists(exampleFile),
		ExampleFile: exampleFile,

		ProviderName:      providerName,
		ProviderShortName: providerShortName(providerName),

		FunctionSignatureMarkdown: signatureComment + "\n" + funcSig,
		FunctionArgumentsMarkdown: argumentComment + "\n" + funcArgs,

		HasVariadic:                      signature.VariadicParameter != nil,
		FunctionVariadicArgumentMarkdown: variadicComment + "\n" + funcVarArg,

		RenderedProviderName: renderedProviderName,

		HasMetadata:  metadataFile != "" && fileExists(metadataFile),
		MetadataFile: metadataFile,
		Metadata:     metadata,
	})
}

const defaultResourceTemplate resourceTemplate = `---
` + frontmatterComment + `
page_title: "{{.Name}} {{.Type}} - {{.ProviderName}}"
subcategory: ""
description: |-
{{ .Description | plainmarkdown | trimspace | prefixlines "  " }}
---

# {{.Name}} ({{.Type}})

{{ .Description | trimspace }}

{{ if .HasExample -}}
## Example Usage

{{tffile .ExampleFile }}
{{- end }}

{{ .SchemaMarkdown | trimspace }}
{{- if .HasImport }}

## Import

Import is supported using the following syntax:

{{codefile "shell" .ImportFile }}
{{- end }}
`

const defaultFunctionTemplate functionTemplate = `---
` + frontmatterComment + `
page_title: "{{.Name}} {{.Type}} - {{.ProviderName}}"
subcategory: ""
description: |-
{{ .Summary | plainmarkdown | trimspace | prefixlines "  " }}
---

# {{.Type}}: {{.Name}}

{{ .Description | trimspace }}

{{ if .HasExample -}}
## Example Usage

{{tffile .ExampleFile }}
{{- end }}

## Signature

{{ .FunctionSignatureMarkdown }}

## Arguments

{{ .FunctionArgumentsMarkdown }}
{{ if .HasVariadic -}}
{{ .FunctionVariadicArgumentMarkdown }}
{{- end }}
`

const defaultProviderTemplate providerTemplate = `---
` + frontmatterComment + `
page_title: "{{.ProviderShortName}} Provider"
subcategory: ""
description: |-
{{ .Description | plainmarkdown | trimspace | prefixlines "  " }}
---

# {{.ProviderShortName}} Provider

{{ .Description | trimspace }}

{{ if .HasExample -}}
## Example Usage

{{tffile .ExampleFile }}
{{- end }}

{{ .SchemaMarkdown | trimspace }}
`

const migrateProviderTemplateComment string = `
{{/* This template serves as a starting point for documentation generation, and can be customized with hardcoded values and/or doc gen templates.

For example, the {{ .SchemaMarkdown }} template can be used to replace manual schema documentation if descriptions of schema attributes are added in the provider source code. */ -}}
`

const migrateFunctionTemplateComment string = `
{{/* This template serves as a starting point for documentation generation, and can be customized with hardcoded values and/or doc gen templates.

For example, the {{ .FunctionArgumentsMarkdown }} template can be used to replace manual argument documentation if descriptions of function arguments are added in the provider source code. */ -}}
`
