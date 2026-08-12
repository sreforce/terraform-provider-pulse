//go:build generate

package tools

import _ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"

// Format Terraform examples used in documentation.
//go:generate terraform fmt -recursive ../examples/

// Generate Terraform Registry documentation from provider schemas and examples.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. -provider-name pulse
