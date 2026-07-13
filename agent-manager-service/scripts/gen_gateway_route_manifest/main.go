// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Default paths are relative to the agent-manager-service module root, which is
// the working directory the Makefile target runs from.
const defaultAPIDir = "api"

var (
	defaultRBACFile = filepath.Join("rbac", "permissions.go")
	defaultOut      = filepath.Join("..", "deployments", "helm-charts", "wso2-agent-manager",
		"files", "amp-api-route-scopes.yaml")
)

func main() {
	apiDir := flag.String("api", defaultAPIDir, "path to the api package directory")
	rbacFile := flag.String("rbac", defaultRBACFile, "path to rbac/permissions.go")
	out := flag.String("out", defaultOut, "path to write the generated manifest")
	flag.Parse()

	if err := run(*apiDir, *rbacFile, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen_gateway_route_manifest:", err)
		os.Exit(1)
	}
}

func run(apiDir, rbacFile, out string) error {
	permFile, err := loadFile(rbacFile)
	if err != nil {
		return fmt.Errorf("load rbac permissions: %w", err)
	}
	perms, err := parsePermissions(permFile)
	if err != nil {
		return fmt.Errorf("parse rbac permissions: %w", err)
	}

	files, err := loadPackageFiles(apiDir)
	if err != nil {
		return fmt.Errorf("load api package: %w", err)
	}
	routes, err := extractRoutes(files, perms)
	if err != nil {
		return err
	}

	manifest, err := renderManifest(routes)
	if err != nil {
		return fmt.Errorf("render manifest: %w", err)
	}
	if err := os.WriteFile(out, manifest, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Printf("wrote %d routes to %s\n", len(routes), out)
	return nil
}

// loadFile parses a single Go source file.
func loadFile(path string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), path, nil, 0)
}

// loadPackageFiles parses every non-test .go file in a package directory.
func loadPackageFiles(dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}
