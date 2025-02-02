// Copyright 2015 Daniel Theophanes.
// Use of this source code is governed by a zlib-style
// license that can be found in the LICENSE file.

package sysvc

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"text/template"
	"time"
)

//go:embed service_rcs_linux.tmpl
var rcsScript string

type rcs struct {
	i        Interface
	platform string
	*Config
}

func isRCS() bool {
	if _, err := os.Stat("/etc/init.d/rcS"); err != nil {
		return false
	}
	if _, err := exec.LookPath("service"); err == nil {
		return false
	}
	if _, err := os.Stat("/etc/inittab"); err == nil {
		filerc, err := os.Open("/etc/inittab")
		if err != nil {
			return false
		}
		defer filerc.Close()

		buf := new(bytes.Buffer)
		buf.ReadFrom(filerc)
		contents := buf.String()

		re := regexp.MustCompile(`::sysinit:.*rcS`)
		matches := re.FindStringSubmatch(contents)
		if len(matches) > 0 {
			return true
		}
		return false
	}
	return false
}

func newRCSService(i Interface, platform string, c *Config) (Service, error) {
	s := &rcs{
		i:        i,
		platform: platform,
		Config:   c,
	}

	return s, nil
}

func (s *rcs) String() string {
	if len(s.DisplayName) > 0 {
		return s.DisplayName
	}
	return s.Name
}

func (s *rcs) Platform() string {
	return s.platform
}

// todo
var errNoUserServiceRCS = errors.New("User services are not supported on rcS.")

func (s *rcs) ConfigPath() (cp string, err error) {
	if s.Option.bool(optionUserService, optionUserServiceDefault) {
		err = errNoUserServiceRCS
		return
	}
	cp = "/etc/init.d/" + s.Config.Name
	return
}

func (s *rcs) template() *template.Template {
	customScript := s.Option.string(optionRCSScript, "")

	if customScript != "" {
		return template.Must(template.New("").Funcs(tf).Parse(customScript))
	}
	return template.Must(template.New("").Funcs(tf).Parse(rcsScript))
}

func (s *rcs) Install() error {
	confPath, err := s.ConfigPath()
	if err != nil {
		return err
	}
	_, err = os.Stat(confPath)
	if err == nil {
		return fmt.Errorf("Init already exists: %s", confPath)
	}

	f, err := os.Create(confPath)
	if err != nil {
		return err
	}
	defer f.Close()

	path, err := s.execPath()
	if err != nil {
		return err
	}

	var to = &struct {
		*Config
		Path         string
		LogDirectory string
	}{
		s.Config,
		path,
		s.Option.string(optionLogDirectory, defaultLogDirectory),
	}

	if err = s.template().Execute(f, to); err != nil {
		return err
	}

	if err = os.Chmod(confPath, 0755); err != nil {
		return err
	}

	if err = os.Symlink(confPath, "/etc/rc.d/S50"+s.Name); err != nil {
		return err
	}

	return nil
}

func (s *rcs) Uninstall() error {
	cp, err := s.ConfigPath()
	if err != nil {
		return err
	}
	if err := os.Remove(cp); err != nil {
		return err
	}
	if err := os.Remove("/etc/rc.d/S50" + s.Name); err != nil {
		return err
	}
	return nil
}

func (s *rcs) Logger(errs chan<- error) (Logger, error) {
	if system.Interactive() {
		return ConsoleLogger, nil
	}
	return s.SystemLogger(errs)
}
func (s *rcs) SystemLogger(errs chan<- error) (Logger, error) {
	return newSysLogger(s.Name, errs)
}

func (s *rcs) Run() (err error) {
	err = s.i.Start(s)
	if err != nil {
		return err
	}

	s.Option.funcSingle(optionRunWait, func() {
		var sigChan = make(chan os.Signal, 3)
		signal.Notify(sigChan, syscall.SIGTERM, os.Interrupt)
		<-sigChan
	})()

	return s.i.Stop(s)
}

func (s *rcs) Status() (Status, error) {
	_, out, err := runWithOutput("/etc/init.d/"+s.Name, "status")
	if err != nil {
		return StatusUnknown, err
	}

	switch {
	case strings.HasPrefix(out, "Running"):
		return StatusRunning, nil
	case strings.HasPrefix(out, "Stopped"):
		return StatusStopped, nil
	default:
		return StatusUnknown, ErrNotInstalled
	}
}

func (s *rcs) Start() error {
	return run("/etc/init.d/"+s.Name, "start")
}

func (s *rcs) Stop() error {
	return run("/etc/init.d/"+s.Name, "stop")
}

func (s *rcs) Restart() error {
	err := s.Stop()
	if err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return s.Start()
}
