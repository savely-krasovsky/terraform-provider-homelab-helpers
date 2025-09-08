// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/hashicorp/terraform-plugin-framework/function"
)

var (
	_ function.Function = DirHashFunction{}
)

func NewDirHashFunction() function.Function {
	return DirHashFunction{}
}

type DirHashFunction struct{}

func (r DirHashFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "dirhash"
}

func (r DirHashFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Calculates the hash of directories matching the given pattern.",
		Parameters: []function.Parameter{
			function.StringParameter{
				AllowNullValue:     false,
				AllowUnknownValues: false,
				Description:        "The path to walk",
				Name:               "path",
			},
			function.StringParameter{
				AllowNullValue:     false,
				AllowUnknownValues: false,
				Description:        "The pattern to match",
				Name:               "pattern",
			},
		},
		Return: function.StringReturn{},
	}
}

func (r DirHashFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var (
		path    string
		pattern string
	)

	resp.Error = function.ConcatFuncErrors(req.Arguments.Get(ctx, &path, &pattern))
	if resp.Error != nil {
		return
	}

	h, err := dirhash(path, pattern)
	if err != nil {
		resp.Error = function.NewFuncError(err.Error())
		return
	}

	resp.Error = function.ConcatFuncErrors(resp.Result.Set(ctx, h))
}

func dirhash(path, pattern string) (string, error) {
	b := new(bytes.Buffer)
	w := zip.NewWriter(b)
	defer func() {
		_ = w.Close()
	}()

	fsys := os.DirFS(path)
	if err := doublestar.GlobWalk(fsys, pattern, func(path string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}

		rf, err := fsys.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			_ = rf.Close()
		}()

		zf, err := w.Create(filepath.ToSlash(path))
		_, err = io.Copy(zf, rf)
		if err != nil {
			return err
		}

		return nil
	}); err != nil {
		return "", err
	}

	if err := w.Close(); err != nil {
		return "", err
	}

	// Calculate hash of archive
	hasher := sha256.New()
	hasher.Write(b.Bytes())
	h := hex.EncodeToString(hasher.Sum(nil))

	return h, nil
}
