/*-
 * Copyright 2015 Grammarly, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package imagename provides docker data structure for docker image names
// It also provides a number of utility functions, related to image name resolving,
// comparison, and semver functionality.
package imagename

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/wmark/semver"
)

const (
	// Latest is :latest tag value
	Latest = "latest"

	// Wildcards is wildcard value variants
	Wildcards = "x*"
)

const (
	// StorageRegistry is when docker registry is used as images storage
	StorageRegistry = "registry"

	// StorageS3 is when s3 registry is used as images storage
	StorageS3 = "s3"
)

const (
	s3Prefix    = "s3.amazonaws.com/"
	s3OldPrefix = "s3:"
)

var (
	ecrRe = regexp.MustCompile("^(\\d+)\\.dkr\\.ecr\\.([^\\.]+)\\.amazonaws\\.com$")
	gcrRe = regexp.MustCompile("^(.*\\.)?gcr.io$")
)

// ImageName is the data structure with describes docker image name
type ImageName struct {
	Registry string
	Name     string
	Tag      string
	Storage  string
	Version  *semver.Range

	IsOldS3Name bool
}

// NewFromString parses a given string and returns ImageName
func NewFromString(image string) *ImageName {
	name, tag := ParseRepositoryTag(image)
	return New(name, tag)
}

// WarnIfOldS3ImageName Check whether old image format is used. Also return warning message if yes
func WarnIfOldS3ImageName(imageName string) (bool, string) {
	if !strings.HasPrefix(imageName, s3OldPrefix) {
		return false, ""
	}

	warning := fmt.Sprintf("Your image '%s' is using old name style (s3:<repo>/<image>) for s3 images."+
		" This style isn't supported by docker 1.10 and would be removed from rocker in the future as well."+
		" Please consider changing to the new schema (s3.amazonaws.com/<repo>/<image>).", imageName)

	return true, warning
}

// New parses a given 'image' and 'tag' strings and returns ImageName
func New(image string, tag string) *ImageName {
	dockerImage := &ImageName{}

	if tag != "" {
		dockerImage.SetTag(tag)
	}

	// default storage driver
	dockerImage.Storage = StorageRegistry

	firstIsHost := false
	prefix := ""

	if strings.HasPrefix(image, s3Prefix) {
		dockerImage.IsOldS3Name = false
		prefix = s3Prefix

	} else if strings.HasPrefix(image, s3OldPrefix) {
		dockerImage.IsOldS3Name = true
		prefix = s3OldPrefix
	}

	if strings.HasPrefix(image, s3Prefix) || strings.HasPrefix(image, s3OldPrefix) {
		image = strings.TrimPrefix(image, prefix)
		dockerImage.Storage = StorageS3
		firstIsHost = true
	}

	nameParts := strings.SplitN(image, "/", 2)

	firstIsHost = firstIsHost ||
		strings.Contains(nameParts[0], ".") ||
		strings.Contains(nameParts[0], ":") ||
		nameParts[0] == "localhost"

	if len(nameParts) == 1 || !firstIsHost {
		dockerImage.Name = image
	} else {
		dockerImage.Registry = nameParts[0]
		dockerImage.Name = nameParts[1]
	}

	// Minor validation
	if dockerImage.Storage == StorageS3 && dockerImage.Registry == "" {
		panic("Image with S3 storage driver requires bucket name to be specified: " + image)
	}

	return dockerImage
}

// ParseRepositoryTag gets a repos name and returns the right reposName + tag|digest
// The tag can be confusing because of a port in a repository name.
//     Ex: localhost.localdomain:5000/samalba/hipache:latest
//     Digest ex: localhost:5000/foo/bar@sha256:bc8813ea7b3603864987522f02a76101c17ad122e1c46d790efc0fca78ca7bfb
// NOTE: borrowed from Docker under Apache 2.0, Copyright 2013-2015 Docker, Inc.
func ParseRepositoryTag(repos string) (string, string) {
	n := strings.Index(repos, "@")
	if n >= 0 {
		parts := strings.Split(repos, "@")
		return parts[0], parts[1]
	}
	n = strings.LastIndex(repos, ":")
	if n < 0 {
		return repos, ""
	}
	if tag := repos[n+1:]; !strings.Contains(tag, "/") {
		return repos[:n], tag
	}
	return repos, ""
}

// String returns the string representation of the current image name
func (img ImageName) String() string {
	if img.TagIsDigest() {
		return img.NameWithRegistry() + "@" + img.GetTag()
	}
	return img.NameWithRegistry() + ":" + img.GetTag()
}

// HasTag returns true if tags is specified for the image name
func (img ImageName) HasTag() bool {
	return img.Tag != ""
}

// TagIsSha returns true if the tag is content addressable sha256 but can also be a tag
// e.g. golang@sha256:ead434cd278824865d6e3b67e5d4579ded02eb2e8367fc165efa21138b225f11
// or golang:sha256-ead434cd278824865d6e3b67e5d4579ded02eb2e8367fc165efa21138b225f11
func (img ImageName) TagIsSha() bool {
	return strings.HasPrefix(img.Tag, "sha256:") || strings.HasPrefix(img.Tag, "sha256-")
}

// TagIsDigest returns true if the tag is content addressable sha256
// e.g. golang@sha256:ead434cd278824865d6e3b67e5d4579ded02eb2e8367fc165efa21138b225f11
func (img ImageName) TagIsDigest() bool {
	return strings.HasPrefix(img.Tag, "sha256:")
}

// GetTag returns the tag of the current image name
func (img ImageName) GetTag() string {
	if img.HasTag() {
		return img.Tag
	}
	return Latest
}

// SetTag sets the new tag for the imagename
func (img *ImageName) SetTag(tag string) {
	img.Version = nil
	if rng, err := semver.NewRange(tag); err == nil && rng != nil {
		img.Version = rng
	}
	img.Tag = tag
}

// IsStrict returns true if tag of the current image is specified and contains no fuzzy rules
// Example:
// golang:latest == true
// golang:stable == true
// golang:1.5.1  == true
// golang:1.5.*  == false
// golang        == false
//
func (img ImageName) IsStrict() bool {
	if img.HasVersionRange() {
		return img.TagAsVersion() != nil
	}
	return img.Tag != ""
}

// All returns true if tag of the current image refers to any version
// Example:
// golang:*      == true
// golang        == true
// golang:latest == false
func (img ImageName) All() bool {
	return strings.Contains(Wildcards, img.Tag)
}

// HasVersion returns true if tag of the current image refers to a semver format
// Example:
// golang:1.5.1  == true
// golang:1.5.*  == false
// golang:stable == false
// golang:latest == false
func (img ImageName) HasVersion() bool {
	return img.TagAsVersion() != nil
}

// HasVersionRange returns true if tag of the current image refers to a semver format and is fuzzy
// Example:
// golang:1.5.1  == true
// golang:1.5.*  == true
// golang:*      == true
// golang:stable == false
// golang:latest == false
// golang        == false
func (img ImageName) HasVersionRange() bool {
	return img.Version != nil
}

// SupportsWildcards returns whether a registry supports tag listing
func (img ImageName) SupportsWildcards() bool {
	return ecrRe.MatchString(img.Registry) ||
		gcrRe.MatchString(img.Registry)
}

// IsECR returns true if the registry is AWS ECR
func (img ImageName) IsECR() bool {
	return ecrRe.MatchString(img.Registry)
}

// GetECRRegion returns the regoin of the AWS ECR registry
func (img ImageName) GetECRRegion() string {
	matches := ecrRe.FindStringSubmatch(img.Registry)
	return matches[2]
}

// TagAsVersion return semver.Version of the current image tag in case it's in semver format
func (img ImageName) TagAsVersion() (ver *semver.Version) {
	ver, _ = semver.NewVersion(strings.TrimPrefix(img.Tag, "v"))
	return
}

// IsSameKind returns true if current image and the given one are same but may have different versions (tags)
func (img ImageName) IsSameKind(b ImageName) bool {
	return img.Registry == b.Registry && img.Name == b.Name
}

// NameWithRegistry returns the [registry/]name of the current image name
func (img ImageName) NameWithRegistry() string {
	registryPrefix := ""
	if img.Registry != "" {
		registryPrefix = img.Registry + "/"
	}
	if img.Storage == StorageS3 {
		if img.IsOldS3Name {
			registryPrefix = s3OldPrefix + registryPrefix
		} else {
			registryPrefix = s3Prefix + registryPrefix
		}
	}
	return registryPrefix + img.Name
}

// Contains returns true if the current image tag wildcard satisfies a given image version
func (img ImageName) Contains(b *ImageName) bool {
	if b == nil {
		return false
	}

	if !img.IsSameKind(*b) {
		return false
	}

	// semver library has a bug with wildcards, so this checks are
	// necessary: empty range (or wildcard range) cannot contain any version, it just fails
	if img.All() {
		return true
	}

	if img.IsStrict() && img.Tag == b.Tag {
		return true
	}

	if img.HasVersionRange() && b.HasVersion() && img.Version.IsSatisfiedBy(b.TagAsVersion()) {
		return true
	}

	return img.Tag == "" && !img.HasVersionRange()
}

// ResolveVersion finds an applicable tag for current image among the list of available tags
func (img *ImageName) ResolveVersion(list []*ImageName, strictS3Match bool) (result *ImageName) {
	for _, candidate := range list {
		// If these are different images (different names/repos)
		if !img.IsSameKind(*candidate) {
			continue
		}

		if strictS3Match && img.IsOldS3Name != candidate.IsOldS3Name {
			continue
		}

		// If we have a strict equality
		if img.HasTag() && candidate.HasTag() && img.Tag == candidate.Tag {
			return candidate
		}

		// If image is without tag, latest will be fine
		if !img.HasTag() && candidate.GetTag() == Latest {
			return candidate
		}

		if !img.Contains(candidate) {
			//this image is from the same name/registry but tag is not applicable
			// e.g. ~1.2.3 contains 1.2.5, but it's not true for 1.3.0
			continue
		}

		if result == nil {
			result = candidate
			continue
		}

		// uncomparable candidate... skipping
		if !candidate.HasVersion() {
			continue
		}

		// if both names has versions to compare, we cat safely compare them
		if result.HasVersion() && candidate.HasVersion() && result.TagAsVersion().Less(candidate.TagAsVersion()) {
			result = candidate
		}
	}

	return
}

// UnmarshalJSON parses JSON string and returns ImageName
func (img *ImageName) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*img = *NewFromString(s)
	return nil
}

// MarshalJSON serializes ImageName to JSON string
func (img ImageName) MarshalJSON() ([]byte, error) {
	return json.Marshal(img.String())
}

// UnmarshalYAML parses YAML string and returns ImageName
func (img *ImageName) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	*img = *NewFromString(s)
	return nil
}

// MarshalYAML serializes ImageName to YAML string
func (img ImageName) MarshalYAML() (interface{}, error) {
	return img.String(), nil
}

// Tags is a structure used for cleaning images
// Sorts out old tags by creation date
type Tags struct {
	Items []*Tag
}

// Tag is a structure used for cleaning images
type Tag struct {
	ID      string
	Name    ImageName
	Created int64
}

// Len returns the length of image tags
func (tags *Tags) Len() int {
	return len(tags.Items)
}

// Less returns true if item by index[i] is created after of item[j]
func (tags *Tags) Less(i, j int) bool {
	return tags.Items[i].Created > tags.Items[j].Created
}

// Swap swaps items by indices [i] and [j]
func (tags *Tags) Swap(i, j int) {
	tags.Items[i], tags.Items[j] = tags.Items[j], tags.Items[i]
}

// GetOld returns the list of items older then [keep] newest items sorted by Created date
func (tags *Tags) GetOld(keep int) []ImageName {
	if len(tags.Items) < keep {
		return nil
	}

	sort.Sort(tags)

	result := []ImageName{}
	for i := keep; i < len(tags.Items); i++ {
		result = append(result, tags.Items[i].Name)
	}

	return result
}
