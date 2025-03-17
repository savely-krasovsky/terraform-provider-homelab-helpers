// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"io/fs"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ function.Function = FilesFunction{}
)

func NewFilesFunction() function.Function {
	return FilesFunction{}
}

type FilesFunction struct{}

func (r FilesFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "files"
}

func (r FilesFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Walks the file tree rooted at root and finds all directories",
		Parameters: []function.Parameter{
			function.StringParameter{
				AllowNullValue:     false,
				AllowUnknownValues: false,
				Description:        "The root to walk",
				Name:               "root",
			},
			function.BoolParameter{
				AllowNullValue:     true,
				AllowUnknownValues: false,
				Description:        "Use unix separators",
				Name:               "unix",
			},
		},
		Return: function.ListReturn{
			ElementType: types.StringType,
		},
	}
}

func (r FilesFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var (
		root string
		unix bool
	)

	resp.Error = function.ConcatFuncErrors(req.Arguments.Get(ctx, &root, &unix))
	if resp.Error != nil {
		return
	}

	resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, files(root, unix)))
}

func files(root string, unix bool) []string {
	ff := make([]string, 0)

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ff
	}

	_ = filepath.Walk(root, func(path string, d fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Get absolute path of current file/directory
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}

		// Get path relative to root directory
		relPath, err := filepath.Rel(absRoot, absPath)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		if !unix {
			ff = append(ff, relPath)
		} else {
			ff = append(ff, filepath.ToSlash(relPath))
		}

		return nil
	})

	return ff
}
