package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/philippwallrafen/gh-actions-quota/internal/quota"
)

const workflowPath = ".github/workflows/gh-actions-quota.yml"

const actionMajor = "v1"

const quotaJobID = "gh-actions-quota"

const previousReusableWorkflow = `name: gh-actions-quota

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
        required: false
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

var reusableWorkflow = strings.Replace(strings.Replace(previousReusableWorkflow,
	"    secrets:\n", `      quota-minutes:
        description: Override included monthly Actions minutes; leave empty for plan detection.
        type: string
        required: false
        default: ""
    secrets:
`, 1), "          threshold: ${{ inputs.threshold }}\n", "          threshold: ${{ inputs.threshold }}\n          quota-minutes: ${{ inputs['quota-minutes'] }}\n", 1)

var workflowQuotaPattern = regexp.MustCompile(`(?m)^        default: "([0-9.eE+-]+)"$`)

func workflowWithQuota(included string) (string, error) {
	if included == "" {
		return reusableWorkflow, nil
	}
	value, err := quota.ParsePositive(included)
	if err != nil {
		return "", err
	}
	return strings.Replace(reusableWorkflow, `default: ""`, fmt.Sprintf(`default: "%g"`, value), 1), nil
}

func recognizedHelper(content string) (string, bool) {
	if content == reusableWorkflow || content == previousReusableWorkflow {
		return "", true
	}
	match := workflowQuotaPattern.FindStringSubmatch(content)
	if len(match) == 2 {
		expected, err := workflowWithQuota(match[1])
		if err == nil && expected == content {
			return match[1], true
		}
	}
	return "", false
}

func workflowQuotaOverride(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(workflowPath)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not inspect %s", workflowPath)
	}
	value, _ := recognizedHelper(string(data))
	return value, nil
}

func ensureReusableWorkflow(root string) (bool, error) {
	return ensureReusableWorkflowWithQuota(root, "")
}

func ensureReusableWorkflowWithQuota(root, included string) (bool, error) {
	content, err := workflowWithQuota(included)
	if err != nil {
		return false, err
	}
	path := filepath.Join(root, filepath.FromSlash(workflowPath))
	existing, err := os.ReadFile(path)
	if err == nil {
		if string(existing) == content || (string(existing) == previousReusableWorkflow && included == "") {
			return false, nil
		}
		if _, recognized := recognizedHelper(string(existing)); recognized && included != "" {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return false, fmt.Errorf("could not inspect %s", workflowPath)
			}
			if err := replaceHelper(path, content, info.Mode().Perm()); err != nil {
				return false, fmt.Errorf("could not update %s", workflowPath)
			}
			return false, nil
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
	if _, err := io.WriteString(file, content); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("could not write %s", workflowPath)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("could not finish writing %s", workflowPath)
	}
	complete = true
	return true, nil
}

func replaceHelper(path, content string, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".gh-actions-quota-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := io.WriteString(file, content); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
