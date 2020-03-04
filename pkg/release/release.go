/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package release

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/blang/semver"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"k8s.io/release/pkg/git"
	"k8s.io/release/pkg/util"
)

const (
	// gcbmgr/anago defaults
	DefaultToolRepo   = "release"
	DefaultToolBranch = "master"
	DefaultProject    = "k8s-staging-release-test"
	DefaultDiskSize   = "300"
	BucketPrefix      = "kubernetes-release-"

	versionReleaseRE  = `v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[a-zA-Z0-9]+)*\.*(0|[1-9][0-9]*)?`
	versionBuildRE    = `([0-9]{1,})\+([0-9a-f]{5,40})`
	versionDirtyRE    = `(-dirty)`
	dockerBuildPath   = "_output/release-tars"
	bazelBuildPath    = "bazel-bin/build/release-tars"
	bazelVersionPath  = "bazel-genfiles/version"
	dockerVersionPath = "kubernetes/version"
	kubernetesTar     = "kubernetes.tar.gz"

	// GCSStagePath is the directory where release artifacts are staged before
	// push to GCS.
	GCSStagePath = "gcs-stage"

	// ReleaseStagePath is the directory where releases are staged.
	ReleaseStagePath = "release-stage"

	// GCEPath is the directory where GCE scripts are created.
	GCEPath = "release-stage/full/kubernetes/cluster/gce"

	// GCIPath is the path for the container optimized OS for GCP.
	GCIPath = "release-stage/full/kubernetes/cluster/gce/gci"

	// ReleaseTarsPath is the directory where release artifacts are created.
	ReleaseTarsPath = "release-tars"

	// WindowsLocalPath is the directory where Windows GCE scripts are created.
	WindowsLocalPath = "release-stage/full/kubernetes/cluster/gce/windows"

	// WindowsGCSPath is the directory where Windoes GCE scripts are staged
	// before push to GCS.
	WindowsGCSPath = "gcs-stage/extra/gce/windows"
)

var (
	DefaultToolOrg = git.DefaultGithubOrg
)

// GetDefaultKubernetesRepoURL returns the default HTTPS repo URL for Release Engineering tools.
// Expected: https://github.com/kubernetes/release
func GetDefaultToolRepoURL() (string, error) {
	return GetToolRepoURL(DefaultToolOrg, DefaultToolRepo, false)
}

// GetKubernetesRepoURL takes a GitHub org and repo, and useSSH as a boolean and
// returns a repo URL for Release Engineering tools.
// Expected result is one of the following:
// - https://github.com/<org>/release
// - git@github.com:<org>/release
func GetToolRepoURL(org, repo string, useSSH bool) (string, error) {
	if org == "" {
		org = GetToolOrg()
	}
	if repo == "" {
		repo = GetToolRepo()
	}

	return git.GetRepoURL(org, repo, useSSH)
}

// GetToolOrg checks if the 'TOOL_ORG' environment variable is set.
// If 'TOOL_ORG' is non-empty, it returns the value. Otherwise, it returns DefaultToolOrg.
func GetToolOrg() string {
	toolOrg := os.Getenv("TOOL_ORG")
	if toolOrg == "" {
		toolOrg = DefaultToolOrg
	}

	return toolOrg
}

// GetToolRepo checks if the 'TOOL_REPO' environment variable is set.
// If 'TOOL_REPO' is non-empty, it returns the value. Otherwise, it returns DefaultToolRepo.
func GetToolRepo() string {
	toolRepo := os.Getenv("TOOL_REPO")
	if toolRepo == "" {
		toolRepo = DefaultToolRepo
	}

	return toolRepo
}

// GetToolBranch checks if the 'TOOL_BRANCH' environment variable is set.
// If 'TOOL_BRANCH' is non-empty, it returns the value. Otherwise, it returns DefaultToolBranch.
func GetToolBranch() string {
	toolBranch := os.Getenv("TOOL_BRANCH")
	if toolBranch == "" {
		toolBranch = DefaultToolBranch
	}

	return toolBranch
}

// BuiltWithBazel determines whether the most recent Kubernetes release was built with Bazel.
func BuiltWithBazel(workDir string) (bool, error) {
	bazelBuild := filepath.Join(workDir, bazelBuildPath, kubernetesTar)
	dockerBuild := filepath.Join(workDir, dockerBuildPath, kubernetesTar)
	return util.MoreRecent(bazelBuild, dockerBuild)
}

// ReadBazelVersion reads the version from a Bazel build.
func ReadBazelVersion(workDir string) (string, error) {
	version, err := ioutil.ReadFile(filepath.Join(workDir, bazelVersionPath))
	return string(version), err
}

// ReadDockerizedVersion reads the version from a Dockerized Kubernetes build.
func ReadDockerizedVersion(workDir string) (string, error) {
	dockerTarball := filepath.Join(workDir, dockerBuildPath, kubernetesTar)
	reader, err := util.ReadFileFromGzippedTar(dockerTarball, dockerVersionPath)
	if err != nil {
		return "", err
	}
	file, err := ioutil.ReadAll(reader)
	return strings.TrimSpace(string(file)), err
}

// IsValidReleaseBuild checks if build version is valid for release.
func IsValidReleaseBuild(build string) (bool, error) {
	return regexp.MatchString("("+versionReleaseRE+`(\.`+versionBuildRE+")?"+versionDirtyRE+"?)", build)
}

// IsDirtyBuild checks if build version is dirty.
func IsDirtyBuild(build string) bool {
	return strings.Contains(build, "dirty")
}

// TODO: Consider collapsing some of these functions.
//       Keeping them as-is for now as kubepkg is dependent on them.
func GetStableReleaseKubeVersion(useSemver bool) (string, error) {
	logrus.Info("Retrieving Kubernetes release version...")
	return GetKubeVersion("https://dl.k8s.io/release/stable.txt", useSemver)
}

func GetStablePrereleaseKubeVersion(useSemver bool) (string, error) {
	logrus.Info("Retrieving Kubernetes testing version...")
	return GetKubeVersion("https://dl.k8s.io/release/latest.txt", useSemver)
}

func GetLatestCIKubeVersion(useSemver bool) (string, error) {
	logrus.Info("Retrieving Kubernetes latest build version...")
	return GetKubeVersion("https://dl.k8s.io/ci/latest.txt", useSemver)
}

func GetCIKubeVersion(branch string, useSemver bool) (string, error) {
	logrus.Infof("Retrieving Kubernetes build version on the '%s' branch...", branch)
	// TODO: We may need to check if the branch exists first to handle the branch cut scenario
	versionMarker := "latest"
	if branch != "master" {
		version := strings.TrimPrefix(branch, "release-")

		versionMarker = fmt.Sprintf("%s-%s", versionMarker, version)
	}

	versionMarkerFile := fmt.Sprintf("%s.txt", versionMarker)
	logrus.Infof("Version marker file: %s", versionMarkerFile)

	u, parseErr := url.Parse("https://dl.k8s.io/ci")
	if parseErr != nil {
		return "", errors.Wrap(parseErr, "failed to parse URL base")
	}

	u.Path = path.Join(u.Path, versionMarkerFile)
	markerURL := u.String()

	return GetKubeVersion(markerURL, useSemver)
}

func GetKubeVersion(markerURL string, useSemver bool) (string, error) {
	logrus.Infof("Retrieving Kubernetes build version from %s...", markerURL)
	version, httpErr := util.GetURLResponse(markerURL, true)
	if httpErr != nil {
		return "", httpErr
	}

	if useSemver {
		// Remove the 'v' prefix from the string to make the version SemVer compliant
		version = strings.TrimPrefix(version, "v")

		sem, semverErr := semver.Parse(version)
		if semverErr != nil {
			return "", semverErr
		}

		version = sem.String()
	}

	logrus.Infof("Retrieved Kubernetes version: %s", version)
	return version, nil
}

// GetKubecrossVersion returns the current kube-cross container version.
// Replaces release::kubecross_version
func GetKubecrossVersion(branches ...string) (string, error) {
	for i, branch := range branches {
		logrus.Infof("Trying to get the kube-cross version for %s...", branch)

		versionURL := fmt.Sprintf("https://raw.githubusercontent.com/kubernetes/kubernetes/%s/build/build-image/cross/VERSION", branch)

		version, httpErr := util.GetURLResponse(versionURL, true)
		if httpErr != nil {
			if i < len(branches)-1 {
				logrus.Infof("Error retrieving the kube-cross version for the '%s': %v", branch, httpErr)
			} else {
				return "", httpErr
			}
		}

		if version != "" {
			logrus.Infof("Found the following kube-cross version: %s", version)
			return version, nil
		}
	}

	return "", errors.New("kube-cross version should not be empty; cannot continue")
}
