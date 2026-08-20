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

package llmprovider

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/spf13/cobra"

	amsvc "github.com/wso2/agent-manager/cli/pkg/clients/amsvc/gen"
	"github.com/wso2/agent-manager/cli/pkg/clierr"
	"github.com/wso2/agent-manager/cli/pkg/cmdutil"
	"github.com/wso2/agent-manager/cli/pkg/iostreams"
	"github.com/wso2/agent-manager/cli/pkg/render"
)

const (
	defaultVersion = "v1"
	defaultContext = "/"
	// defaultAuthType matches the auth scheme of the built-in templates that
	// require a credential (openai, anthropic, mistralai, …). It is only sent
	// when the user also supplies a key or an explicit auth override.
	defaultAuthType = "api-key"
)

// validAuthTypes are the upstream auth schemes accepted by the service.
var validAuthTypes = []string{"api-key", "none"}

type CreateOptions struct {
	IO           *iostreams.IOStreams
	Client       func(context.Context) (*amsvc.ClientWithResponses, error)
	ResolveScope func(*cobra.Command, bool, bool) (string, string, error)
	MakeScope    func(org, proj string) render.Scope

	Org   string
	Scope render.Scope

	ID          string
	DisplayName string
	Version     string
	Context     string
	Template    string
	Description string

	// Upstream overrides. When omitted, the provider inherits the template's
	// endpoint URL and auth scheme.
	UpstreamURL   string
	AuthType      string
	AuthHeader    string
	AuthTypeSet   bool
	AuthHeaderSet bool

	APIKey      string
	APIKeyStdin bool

	Gateways []string
}

// keyRequested reports whether the user asked to attach a credential, without
// reading stdin (so it is safe to call during validation).
func (o *CreateOptions) keyRequested() bool {
	return o.APIKey != "" || o.APIKeyStdin
}

func validateCreate(opts *CreateOptions) error {
	var v []string

	if opts.ID == "" {
		v = append(v, "id argument is required")
	} else if msg := handleViolation(opts.ID); msg != "" {
		v = append(v, msg)
	}
	if opts.DisplayName == "" {
		v = append(v, "--display-name is required")
	}
	if opts.Template == "" {
		v = append(v, "--template is required")
	}
	if msg := contextViolation(opts.Context); msg != "" {
		v = append(v, msg)
	}
	if !isValidAuthType(opts.AuthType) {
		v = append(v, fmt.Sprintf("--auth-type must be one of: %s", strings.Join(validAuthTypes, ", ")))
	}
	if opts.APIKey != "" && opts.APIKeyStdin {
		v = append(v, "--api-key and --api-key-stdin are mutually exclusive")
	}
	if opts.APIKey != "" && strings.TrimSpace(opts.APIKey) == "" {
		v = append(v, "--api-key must not be blank")
	}
	if opts.keyRequested() && opts.AuthType == "none" {
		v = append(v, "an API key cannot be used with --auth-type none")
	}
	if _, err := parseGateways(opts.Gateways); err != nil {
		v = append(v, err.Error())
	}

	if len(v) == 0 {
		return nil
	}
	return cmdutil.FlagErrors(v)
}

func isValidAuthType(t string) bool {
	return slices.Contains(validAuthTypes, t)
}

// handleRegex matches a valid resource handle: letters, digits, '-' and '_'.
// It mirrors the server-side rule (utils.ValidateTemplateHandle).
var handleRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// handleViolation returns a validation message when id is not a valid resource
// handle, or "" when it is valid: at most 255 characters and limited to
// letters, digits, '-' and '_'. The empty case is reported separately so the
// caller can emit the "id argument is required" message.
func handleViolation(id string) string {
	if len(id) > 255 {
		return "id must be at most 255 characters"
	}
	if !handleRegex.MatchString(id) {
		return "id must contain only letters, digits, '-' or '_'"
	}
	return ""
}

// contextViolation returns a validation message when the API context path is
// malformed, or "" when valid. The path must start with '/' and must not end
// with '/', except the root path "/". This mirrors the console's validation.
func contextViolation(ctx string) string {
	if !strings.HasPrefix(ctx, "/") {
		return "--context must start with '/'"
	}
	if ctx != "/" && strings.HasSuffix(ctx, "/") {
		return "--context must not end with '/'"
	}
	return ""
}

// parseGateways converts the raw --gateways values into typed UUIDs, reporting
// the first malformed value.
func parseGateways(raw []string) ([]openapi_types.UUID, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]openapi_types.UUID, 0, len(raw))
	for _, g := range raw {
		id, err := uuid.Parse(strings.TrimSpace(g))
		if err != nil {
			return nil, fmt.Errorf("invalid gateway id %q: must be a UUID", g)
		}
		out = append(out, id)
	}
	return out, nil
}

func NewCreateCmd(f *cmdutil.Factory) *cobra.Command {
	opts := &CreateOptions{
		IO:           f.IOStreams,
		Client:       f.AgentManager,
		ResolveScope: f.ResolveOrgProject,
		MakeScope:    f.Scope,
	}
	cmd := &cobra.Command{
		Use:   "create <id>",
		Short: "Create a new LLM provider",
		Long: "Create a new LLM provider in an organization.\n\n" +
			"The endpoint URL and auth scheme are inherited from the chosen --template; " +
			"override them with --upstream-url/--auth-type/--auth-header only when needed. " +
			"Supply the provider credential with --api-key-stdin (recommended) or --api-key.\n\n" +
			"Built-in --template handles: anthropic, awsbedrock, azure-openai, azureai-foundry, " +
			"gemini, mistralai, openai. Shell completion on --template lists the live set " +
			"(built-in plus any custom templates) for your org.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.ID = args[0]
			}
			opts.AuthTypeSet = cmd.Flags().Changed("auth-type")
			opts.AuthHeaderSet = cmd.Flags().Changed("auth-header")

			if err := validateCreate(opts); err != nil {
				return render.Error(opts.IO, render.Scope{}, err)
			}
			org, _, err := opts.ResolveScope(cmd, true, false)
			scope := opts.MakeScope(org, "")
			if err != nil {
				return render.Error(opts.IO, scope, err)
			}
			opts.Org, opts.Scope = org, scope
			return runCreate(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.DisplayName, "display-name", "", "Human-readable display name (required)")
	cmd.Flags().StringVar(&opts.Template, "template", "", "Provider template handle, e.g. openai, anthropic, mistralai (required)")
	cmd.Flags().StringVar(&opts.Version, "version", defaultVersion, "Provider version")
	cmd.Flags().StringVar(&opts.Context, "context", defaultContext, "API context path (must start with /, no trailing slash)")
	cmd.Flags().StringVar(&opts.Description, "description", "", "Provider description")
	cmd.Flags().StringVar(&opts.UpstreamURL, "upstream-url", "", "Override the template's upstream endpoint URL")
	cmd.Flags().StringVar(&opts.AuthType, "auth-type", defaultAuthType, "Upstream auth type: api-key, basic, bearer, or none")
	cmd.Flags().StringVar(&opts.AuthHeader, "auth-header", "", "Override the template's auth header name")
	cmd.Flags().StringVar(&opts.APIKey, "api-key", "", "Provider API key (leaks into shell history; prefer --api-key-stdin)")
	cmd.Flags().BoolVar(&opts.APIKeyStdin, "api-key-stdin", false, "Read the provider API key from stdin")
	cmd.Flags().StringSliceVar(&opts.Gateways, "gateways", nil, "Gateway UUIDs to deploy the provider to (repeatable)")

	_ = cmd.RegisterFlagCompletionFunc("auth-type", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return validAuthTypes, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("template", func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return cmdutil.CompleteLLMProviderTemplates(cmd, f), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func runCreate(ctx context.Context, o *CreateOptions) error {
	key := o.APIKey
	if o.APIKeyStdin {
		data, err := io.ReadAll(o.IO.In)
		if err != nil {
			return render.Error(o.IO, o.Scope, clierr.Newf(clierr.InvalidFlag, "read API key from stdin: %v", err))
		}
		key = strings.TrimSpace(string(data))
		if key == "" {
			return render.Error(o.IO, o.Scope, clierr.New(clierr.InvalidFlag, "no API key provided on stdin"))
		}
	}

	req, err := buildCreateRequest(o, key)
	if err != nil {
		return render.Error(o.IO, o.Scope, err)
	}

	client, err := o.Client(ctx)
	if err != nil {
		return render.Error(o.IO, o.Scope, err)
	}

	resp, err := client.CreateLLMProviderWithResponse(ctx, o.Org, req)
	if err != nil {
		return render.Error(o.IO, o.Scope, clierr.Newf(clierr.Transport, "%v", err))
	}
	if resp.JSON201 == nil {
		return render.Error(o.IO, o.Scope, cmdutil.ErrorFromServer(resp.HTTPResponse, cmdutil.FirstNonNil(resp.JSON400, resp.JSON401, resp.JSON409, resp.JSON500)))
	}

	if o.IO.JSON {
		return render.JSONSuccess(o.IO, o.Scope, resp.JSON201)
	}

	printProviderSummary(o.IO, resp.JSON201)
	return nil
}

// buildCreateRequest maps the resolved options into the create payload. The
// upstream block is attached only when the user supplies a URL or an auth input,
// so a bare `create <id> --display-name --template` defers entirely to the template.
func buildCreateRequest(o *CreateOptions, key string) (amsvc.CreateLLMProviderRequest, error) {
	req := amsvc.CreateLLMProviderRequest{
		Id:       o.ID,
		Name:     o.DisplayName,
		Version:  o.Version,
		Context:  o.Context,
		Template: o.Template,
	}
	if o.Description != "" {
		req.Description = &o.Description
	}

	keyProvided := key != ""
	attachAuth := keyProvided || o.AuthTypeSet || o.AuthHeaderSet
	if attachAuth || o.UpstreamURL != "" {
		main := &amsvc.UpstreamEndpoint{}
		if o.UpstreamURL != "" {
			main.Url = &o.UpstreamURL
		}
		if attachAuth {
			auth := &amsvc.UpstreamAuth{Type: amsvc.UpstreamAuthType(o.AuthType)}
			if o.AuthHeader != "" {
				auth.Header = &o.AuthHeader
			}
			if keyProvided {
				auth.Value = &key
			}
			main.Auth = auth
		}
		req.Upstream = amsvc.UpstreamConfig{Main: main}
	}

	gws, err := parseGateways(o.Gateways)
	if err != nil {
		return req, cmdutil.FlagErrors([]string{err.Error()})
	}
	if len(gws) > 0 {
		req.Gateways = &gws
	}

	return req, nil
}

func printProviderSummary(ios *iostreams.IOStreams, p *amsvc.LLMProviderResponse) {
	cs := ios.StderrColorScheme()
	fmt.Fprintf(ios.ErrOut, "%s Created LLM provider %s\n\n", cs.SuccessIcon(), p.Id)
	fmt.Fprintf(ios.ErrOut, "  Name:     %s\n", p.Name)
	fmt.Fprintf(ios.ErrOut, "  Template: %s\n", p.Template)
	fmt.Fprintf(ios.ErrOut, "  Status:   %s\n", string(p.Status))
}
