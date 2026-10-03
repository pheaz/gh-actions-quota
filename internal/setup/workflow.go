package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const workflowPath = ".github/workflows/gh-actions-quota.yml"

const actionMajor = "v1"

const quotaJobID = "gh-actions-quota"

const reusableWorkflow = `name: gh-actions-quota

on:
  workflow_call:
    inputs:
      threshold:
        description: Stop allowing gated jobs once usage reaches this percentage.
        type: number
        required: false
        default: 50
    secrets:
      ACTIONS_QUOTA_TOKEN:
        required: true
    outputs:
      allowed:
        description: Whether gated jobs may run.
        value: ${{ jobs.gh-actions-quota.outputs.allowed }}
      usage_available:
        description: Whether billing usage could be determined.
        value: ${{ jobs.gh-actions-quota.outputs.usage_available }}
      usage_percent:
        description: Percent of the included Actions quota consumed.
        value: ${{ jobs.gh-actions-quota.outputs.usage_percent }}

permissions:
  contents: read

jobs:
  gh-actions-quota:
    name: gh-actions-quota
    runs-on: ubuntu-slim
    outputs:
      allowed: ${{ steps.gh-actions-quota.outputs.allowed }}
      usage_available: ${{ steps.gh-actions-quota.outputs['usage-available'] }}
      usage_percent: ${{ steps.gh-actions-quota.outputs['usage-percent'] }}

    steps:
      - uses: philippwallrafen/gh-actions-quota@` + actionMajor + `
        id: gh-actions-quota
        with:
          token: ${{ secrets.ACTIONS_QUOTA_TOKEN }}
          threshold: ${{ inputs.threshold }}
`

// Match the previous generated template exactly; customized files remain protected.
var legacyReusableWorkflow = strings.NewReplacer(
	"name: gh-actions-quota\n\non:", "name: GitHub Actions quota\n\non:",
	"jobs.gh-actions-quota.outputs", "jobs.quota.outputs",
	"  gh-actions-quota:\n", "  quota:\n",
	"    name: gh-actions-quota", "    name: CI quota control",
	"steps.gh-actions-quota.outputs", "steps.quota.outputs",
	"id: gh-actions-quota", "id: quota",
).Replace(reusableWorkflow)

func ensureReusableWorkflow(root string) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(workflowPath))
	existing, err := os.ReadFile(path)
	if err == nil {
		if string(existing) == reusableWorkflow {
			return false, nil
		}
		if string(existing) == legacyReusableWorkflow {
			info, err := os.Stat(path)
			if err != nil {
				return false, fmt.Errorf("could not inspect %s", workflowPath)
			}
			if err := os.WriteFile(path, []byte(reusableWorkflow), info.Mode().Perm()); err != nil {
				return false, fmt.Errorf("could not migrate %s", workflowPath)
			}
			return true, nil
		}
		return false, fmt.Errorf("%s already exists and differs from the generated template; reconcile or remove it before running setup again", workflowPath)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("could not inspect %s", workflowPath)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("could not create the workflow directory for %s", workflowPath)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false, fmt.Errorf("could not create %s", workflowPath)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.WriteString(file, reusableWorkflow); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("could not write %s", workflowPath)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("could not finish writing %s", workflowPath)
	}
	complete = true
	return true, nil
}
