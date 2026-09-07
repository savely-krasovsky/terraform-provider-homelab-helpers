// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"archive/zip"
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

var _ function.Function = dirHashFunction{}

type dirHashFunction struct{}

func (r dirHashFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "dirhash"
}

func (r dirHashFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
	resp.Definition = function.Definition{
		Summary: "Calculates the hash of directories matching the given pattern.",
		Parameters: []function.Parameter{
			function.StringParameter{
				Description: "The path to walk",
				Name:        "path",
			},
			function.StringParameter{
				Description: "The pattern to match",
				Name:        "pattern",
			},
		},
		Return: function.StringReturn{},
	}
}

func (r dirHashFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var (
		path    string
		pattern string
	)

	resp.Error = req.Arguments.Get(ctx, &path, &pattern)
	if resp.Error != nil {
		return
	}

	hash, err := dirhash(path, pattern)
	if err != nil {
		resp.Error = function.NewFuncError(err.Error())
		return
	}

	resp.Error = resp.Result.Set(ctx, hash)
}

func dirhash(path, pattern string) (string, error) {
	hasher := sha256.New()
	archive := zip.NewWriter(hasher)
	defer archive.Close()

	fsys := os.DirFS(path)
	if err := doublestar.GlobWalk(fsys, pattern, func(path string, d fs.DirEntry) error {
		if d.IsDir() {
			return nil
		}

		file, err := fsys.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		entry, err := archive.Create(filepath.ToSlash(path))
		if err != nil {
			return err
		}

		_, err = io.Copy(entry, file)

		return err
	}); err != nil {
		return "", err
	}

	if err := archive.Close(); err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
