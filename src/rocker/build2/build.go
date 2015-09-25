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

package build2

import (
	"io"

	"github.com/fatih/color"

	"github.com/fsouza/go-dockerclient"
	"github.com/kr/pretty"

	log "github.com/Sirupsen/logrus"
)

var (
	NoBaseImageSpecifier = "scratch"
	MountVolumeImage     = "grammarly/scratch:latest"
	RsyncImage           = "grammarly/rsync-static:1"
	ExportsPath          = "/.rocker_exports"
)

type Config struct {
	OutStream    io.Writer
	InStream     io.ReadCloser
	ContextDir   string
	ID           string
	Dockerignore []string
	Pull         bool
	NoGarbage    bool
	Attach       bool
	Verbose      bool
}

type State struct {
	Config         docker.Config
	HostConfig     docker.HostConfig
	ImageID        string
	ContainerID    string
	ExportsID      string
	CommitMsg      []string
	ProducedImage  bool
	CmdSet         bool
	InjectCommands []string
	Dockerignore   []string
}

type Build struct {
	ProducedSize int64
	VirtualSize  int64

	rockerfile *Rockerfile
	cfg        Config
	client     Client
	state      State
}

func New(client Client, rockerfile *Rockerfile, cfg Config) *Build {
	b := &Build{
		rockerfile: rockerfile,
		cfg:        cfg,
		client:     client,
	}
	b.state = b.NewState()
	return b
}

func (b *Build) NewState() State {
	return State{
		Dockerignore: b.cfg.Dockerignore,
	}
}

func (b *Build) Run(plan Plan) (err error) {

	for k := 0; k < len(plan); k++ {
		c := plan[k]

		log.Debugf("Step %d: %# v", k+1, pretty.Formatter(c))
		log.Infof("%s", color.New(color.FgWhite, color.Bold).SprintFunc()(c))

		if b.state, err = c.Execute(b); err != nil {
			return err
		}

		log.Debugf("State after step %d: %# v", k+1, pretty.Formatter(b.state))

		// Here we need to inject ONBUILD commands on the fly,
		// build sub plan and merge it with the main plan.
		// Not very beautiful, because Run uses Plan as the argument
		// and then it builds its own. But.
		if len(b.state.InjectCommands) > 0 {
			commands, err := parseOnbuildCommands(b.state.InjectCommands)
			if err != nil {
				return err
			}
			subPlan, err := NewPlan(commands, false)
			if err != nil {
				return err
			}
			tail := append(subPlan, plan[k+1:]...)
			plan = append(plan[:k+1], tail...)

			b.state.InjectCommands = []string{}
		}
	}

	return nil
}

func (b *Build) GetState() State {
	return b.state
}

func (b *Build) GetImageID() string {
	return b.state.ImageID
}

func (b *Build) getVolumeContainer(path string) (name string, err error) {

	name = b.mountsContainerName(path)

	config := &docker.Config{
		Image: MountVolumeImage,
		Volumes: map[string]struct{}{
			path: struct{}{},
		},
	}

	log.Debugf("Make MOUNT volume container %s with options %# v", name, config)

	if _, err = b.client.EnsureContainer(name, config, path); err != nil {
		return name, err
	}

	log.Infof("| Using container %s for %s", name, path)

	return name, nil
}

func (b *Build) getExportsContainer() (name string, err error) {
	name = b.exportsContainerName()

	config := &docker.Config{
		Image: RsyncImage,
		Volumes: map[string]struct{}{
			"/opt/rsync/bin": struct{}{},
			ExportsPath:      struct{}{},
		},
	}

	log.Debugf("Make EXPORT container %s with options %# v", name, config)

	containerID, err := b.client.EnsureContainer(name, config, "exports")
	if err != nil {
		return "", err
	}

	log.Infof("| Using exports container %s", name)

	return containerID, nil
}

func (s *State) Commit(msg string) {
	s.CommitMsg = append(s.CommitMsg, msg)
}

func (s *State) SkipCommit() {
	s.Commit(COMMIT_SKIP)
}
