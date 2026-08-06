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

package openchoreo

import (
	"errors"

	secretmanagersvc "github.com/wso2/agent-manager/agent-manager-service/clients/secretmanagersvc"
)

// ProviderName is the name used to register this provider.
const ProviderName = "openchoreo"

// Provider implements the secretmanagersvc.Provider interface backed by the
// OpenChoreo secret management API. The API stores secret values in the
// target plane's secret store and manages the underlying SecretReference CRs
// itself, so no direct KV access is needed.
type Provider struct{}

// Ensure Provider implements the interface.
var _ secretmanagersvc.Provider = &Provider{}

// NewProvider creates a new OpenChoreo provider instance.
func NewProvider() secretmanagersvc.Provider {
	return &Provider{}
}

// Capabilities returns the provider's capabilities.
func (p *Provider) Capabilities() secretmanagersvc.StoreCapabilities {
	return secretmanagersvc.StoreCapabilityReadWrite
}

// NewClient creates a new SecretsClient backed by the OpenChoreo secret API.
func (p *Provider) NewClient(config *secretmanagersvc.StoreConfig) (secretmanagersvc.SecretsClient, error) {
	if err := p.ValidateConfig(config); err != nil {
		return nil, err
	}

	return &Client{
		oc:              config.OpenChoreo.Client,
		targetPlaneKind: config.OpenChoreo.TargetPlaneKind,
		targetPlaneName: config.OpenChoreo.TargetPlaneName,
	}, nil
}

// ValidateConfig validates the OpenChoreo provider configuration.
func (p *Provider) ValidateConfig(config *secretmanagersvc.StoreConfig) error {
	if config == nil {
		return errors.New("config is required")
	}
	if config.OpenChoreo == nil {
		return errors.New("openchoreo config is required")
	}
	if config.OpenChoreo.Client == nil {
		return errors.New("openchoreo client is required")
	}
	return nil
}
