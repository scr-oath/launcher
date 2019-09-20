package executor

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/screwdriver-cd/launcher/mock_screwdriver"
	"github.com/screwdriver-cd/launcher/screwdriver"
)

const TestBuildTimeout = 60

var stepFilePath = "/tmp/step.sh"

func IgnoringWrite(p []byte) (n int, err error) {
	return len(p), nil
}

func NewDoNothingEmitter(ctrl *gomock.Controller) *mock_screwdriver.MockEmitter {
	testEmitter := mock_screwdriver.NewMockEmitter(ctrl)
	testEmitter.EXPECT().
		StartCmd(gomock.Any()).
		AnyTimes()
	testEmitter.EXPECT().
		Write(gomock.Any()).
		DoAndReturn(IgnoringWrite).
		AnyTimes()
	testEmitter.EXPECT().
		Close().
		AnyTimes()
	return testEmitter
}

func NewDoNothingAPI(ctrl *gomock.Controller) *mock_screwdriver.MockAPI {
	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	testAPI.EXPECT().
		UpdateStepStart(gomock.Any(), gomock.Any()).
		AnyTimes()
	testAPI.EXPECT().
		UpdateStepStop(gomock.Any(), gomock.Any(), gomock.Any()).
		AnyTimes()
	return testAPI
}

func TestHelperProcess(*testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args[:]
	for i, val := range os.Args { // Should become something lke ["git", "clone"]
		args = os.Args[i:]
		if val == "--" {
			args = args[1:]
			break
		}
	}

	if strings.HasPrefix(args[2], "source") {
		os.Exit(0)
	}

	os.Exit(255)
}

func cleanup(filename string) {
	_, err := os.Stat(filename)

	if err == nil {
		os.Remove(filename)
	}
}

func setupTestCase(t *testing.T, filename string) {
	t.Log("setup test case")
	cleanup(filename)
	cleanup(filename + "_tmp")
	cleanup(filename + "_export")
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func ReadCommand(file string) []string {
	// read the file that was written for the command
	fileread, err := ioutil.ReadFile(file)
	if err != nil {
		panic(fmt.Errorf("Couldn't read file: %v", err))
	}

	return strings.Split(string(fileread), "\n")
}

func TestUnmocked(t *testing.T) {
	var tests = []struct {
		command string
		err     error
		shell   string
	}{
		{"ls", nil, "/bin/sh"},
		{"sleep 1", nil, "/bin/sh"},
		{"ls && ls ", nil, "/bin/sh"},
		// Large single-line
		{"openssl rand -hex 1000000", nil, "/bin/sh"},
		{"doesntexist", fmt.Errorf("Launching command exit with code: %v", 127), "/bin/sh"},
		{"ls && sh -c 'exit 5' && sh -c 'exit 2'", fmt.Errorf("Launching command exit with code: %v", 5), "/bin/sh"},
		// Custom shell
		{"ls", nil, "/bin/bash"},
	}

	for index, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			envFilepath := "/tmp/testUnmocked" + strconv.Itoa(index)
			setupTestCase(t, envFilepath)
			cmd := screwdriver.CommandDef{
				Cmd:  test.command,
				Name: "test",
			}
			testBuild := screwdriver.Build{
				ID: 12345,
				Commands: []screwdriver.CommandDef{
					cmd,
				},
				Environment: []map[string]string{},
			}
			testAPI := mock_screwdriver.NewMockAPI(ctrl)
			testAPI.EXPECT().
				UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq("test"))
			testAPI.EXPECT().
				UpdateStepStop(gomock.Any(), gomock.Eq("test"), gomock.Any())
			testEmitter := NewDoNothingEmitter(ctrl)

			err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, test.shell, TestBuildTimeout, envFilepath, "")
			commands := ReadCommand(stepFilePath)

			if !reflect.DeepEqual(err, test.err) {
				t.Fatalf("Unexpected error: (%v) - should be (%v)", err, test.err)
			}

			if test.shell == "" {
				test.shell = "/bin/sh"
			}

			if !reflect.DeepEqual(commands[0], "#!"+test.shell+" -e") {
				t.Errorf("Unexpected shell from Run(%#v): %v", test, commands[0])
			}

			if !reflect.DeepEqual(commands[1], test.command) {
				t.Errorf("Unexpected command from Run(%#v): %v", test, commands[1])
			}
		})
	}
}

func TestMulti(t *testing.T) {
	envFilepath := "/tmp/testMulti"
	setupTestCase(t, envFilepath)

	commands := []screwdriver.CommandDef{
		{Cmd: "ls", Name: "test ls"},
		{Cmd: "export FOO=BAR", Name: "test export env"},
		{Cmd: `if [ -z "$FOO" ] ; then exit 1; fi`, Name: "test if env set"},
		{Cmd: "doesnotexist", Name: "test doesnotexist err"},
		{Cmd: "echo user teardown step", Name: "teardown-echo"},
		{Cmd: "sleep 1", Name: "test sleep 1"},
		{Cmd: "echo upload artifacts", Name: "sd-teardown-artifacts"},
	}
	testBuild := screwdriver.Build{
		ID:          12345,
		Commands:    commands,
		Environment: []map[string]string{},
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	seenDoesnotexist := false
	for _, cmd := range commands {
		if cmd.Cmd == "doesnotexist" {
			testAPI.EXPECT().
				UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name))
			testAPI.EXPECT().
				UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name), gomock.Not(gomock.Eq(0)))
			seenDoesnotexist = true
		} else if !seenDoesnotexist || strings.Contains(cmd.Name, "teardown") {
			testAPI.EXPECT().
				UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name))
			testAPI.EXPECT().
				UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name), gomock.Eq(0))
		}
	}
	testEmitter := NewDoNothingEmitter(ctrl)

	err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/sh", TestBuildTimeout, envFilepath, "")
	expectedErr := fmt.Errorf("Launching command exit with code: %v", 127)
	if !reflect.DeepEqual(err, expectedErr) {
		t.Fatalf("Unexpected error: %v - should be %v", err, expectedErr)
	}
}

func TestTeardownEnv(t *testing.T) {
	envFilepath := "/tmp/testTeardownEnv"
	setupTestCase(t, envFilepath)

	baseEnv := []string{
		"com.apple=test",
		"var0=xxx",
		"var1=foo",
		"var2=bar",
		"VAR3=baz",
	}

	commands := []screwdriver.CommandDef{
		{Cmd: "export FOO=\"BAR with spaces\"", Name: "foo"},
		{Cmd: "export SINGLE_QUOTE=\"my ' single quote\"", Name: "singlequote"},
		{Cmd: "export DOUBLE_QUOTE=\"my \\\" double quote\"", Name: "doublequote"},
		{Cmd: "export NEWLINE=\"new\\nline\"", Name: "newline"},
		{Cmd: "doesnotexist", Name: "doesnotexist"},
		{Cmd: "echo bye", Name: "preteardown-foo"},
		{Cmd: "if [ \"$FOO\" != 'BAR with spaces' ]; then exit 1; fi", Name: "teardown-foo"},
		{Cmd: "if [ \"$SINGLE_QUOTE\" != \"my ' single quote\" ]; then exit 1; fi", Name: "teardown-singlequote"},
		{Cmd: "if [ \"$DOUBLE_QUOTE\" != \"my \\\" double quote\" ]; then exit 1; fi", Name: "sd-teardown-doublequote"},
		{Cmd: "if [ \"$NEWLINE\" != \"new\\nline\" ]; then exit 1; fi", Name: "sd-teardown-newline"},
		{Cmd: "if [ \"$VAR3\" != \"baz\" ]; then exit 1; fi", Name: "sd-teardown-baseenv"},
	}
	testBuild := screwdriver.Build{
		ID:          12345,
		Commands:    commands,
		Environment: []map[string]string{},
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	seenDoesnotexist := false
	for _, cmd := range commands {
		if cmd.Cmd == "doesnotexist" {
			testAPI.EXPECT().
				UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name))
			testAPI.EXPECT().
				UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name), gomock.Not(gomock.Eq(0)))
			seenDoesnotexist = true
		} else if !seenDoesnotexist || strings.Contains(cmd.Name, "teardown") {
			testAPI.EXPECT().
				UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name))
			testAPI.EXPECT().
				UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name), gomock.Eq(0))
		}
	}

	testEmitter := NewDoNothingEmitter(ctrl)
	err := Run("", baseEnv, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/sh", TestBuildTimeout, envFilepath, "")
	expectedErr := fmt.Errorf("Launching command exit with code: %v", 127)
	if !reflect.DeepEqual(err, expectedErr) {
		t.Fatalf("Unexpected error: %v - should be %v", err, expectedErr)
	}
}

func TestTeardownfail(t *testing.T) {
	envFilepath := "/tmp/testTeardownfail"
	setupTestCase(t, envFilepath)
	commands := []screwdriver.CommandDef{
		{Cmd: "ls", Name: "test ls"},
		{Cmd: "doesnotexist", Name: "sd-teardown-artifacts"},
	}
	testBuild := screwdriver.Build{
		ID:          12345,
		Commands:    commands,
		Environment: []map[string]string{},
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	testAPI.EXPECT().
		UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Any()).
		AnyTimes()
	testAPI.EXPECT().
		UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Any(), gomock.Any()).
		AnyTimes()
	testEmitter := NewDoNothingEmitter(ctrl)

	err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/sh", TestBuildTimeout, envFilepath, "")
	expectedErr := ErrStatus{127}
	if !reflect.DeepEqual(err, expectedErr) {
		t.Fatalf("Unexpected error: %v - should be %v", err, expectedErr)
	}
}

type cmdMatcher struct {
	cmd string
}

func (c *cmdMatcher) Matches(x interface{}) bool {
	return x.(screwdriver.CommandDef).Cmd == c.cmd
}

func (c *cmdMatcher) String() string {
	return "cmd.Cmd == " + c.cmd
}

func NewCmdCmdMatcher(cmd string) *cmdMatcher {
	return &cmdMatcher{cmd: cmd}
}

type cmdNameMatcher struct {
	name string
}

func (c *cmdNameMatcher) Matches(x interface{}) bool {
	return x.(screwdriver.CommandDef).Name == c.name
}

func (c *cmdNameMatcher) String() string {
	return "cmd.Name == " + c.name
}

func NewCmdNameMatcher(name string) *cmdNameMatcher {
	return &cmdNameMatcher{name: name}
}

func TestTimeout(t *testing.T) {
	envFilepath := "/tmp/testTimeout"
	setupTestCase(t, envFilepath)
	commands := []screwdriver.CommandDef{
		{Cmd: "echo testing timeout", Name: "test timeout"},
		{Cmd: "sleep 3", Name: "sleep for a long time"},
		{Cmd: "echo woke up to snooze", Name: "snooze"},
		{Cmd: "sleep 3", Name: "sleep for another long time"},
		{Cmd: "exit 0", Name: "completed"},
	}
	testBuild := screwdriver.Build{
		ID:          12345,
		Commands:    commands,
		Environment: []map[string]string{},
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	testEmitter := mock_screwdriver.NewMockEmitter(ctrl)
	testEmitter.EXPECT().
		Write(gomock.Any()).
		DoAndReturn(IgnoringWrite).
		AnyTimes()
	testEmitter.EXPECT().
		Close().
		AnyTimes()

	seenSleep3 := false
	for _, cmd := range commands {
		if !seenSleep3 || strings.Contains(cmd.Name, "teardown") {
			if cmd.Cmd == "sleep 3" {
				testAPI.EXPECT().
					UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name))
				testAPI.EXPECT().
					UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name), gomock.Not(gomock.Eq(0)))
				testEmitter.EXPECT().
					StartCmd(NewCmdCmdMatcher(cmd.Cmd)).
					Do(func(_ interface{}) {
						time.Sleep(10000)
					})
				seenSleep3 = true
			} else {
				testAPI.EXPECT().
					UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name))
				testAPI.EXPECT().
					UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq(cmd.Name), gomock.Eq(0))
				testEmitter.EXPECT().
					StartCmd(NewCmdCmdMatcher(cmd.Cmd))
			}
		}
	}

	testTimeout := 3
	err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/sh", testTimeout, envFilepath, "")
	expectedErr := fmt.Errorf("Timeout of %vs seconds exceeded", testTimeout)
	if !reflect.DeepEqual(err, expectedErr) {
		t.Fatalf("Unexpected error: %v - should be %v", err, expectedErr)
	}
}

func TestEnv(t *testing.T) {
	envFilepath := "/tmp/testEnv"
	setupTestCase(t, envFilepath)
	baseEnv := []string{
		"com.apple=test",
		"var0=xxx",
		"var1=foo",
		"var2=bar",
		"VAR3=baz",
	}

	want := map[string]string{
		"var1": "foo",
		"var2": "bar",
		"VAR3": "baz",
	}

	cmds := []screwdriver.CommandDef{
		{
			Cmd:  "env",
			Name: "test",
		},
	}

	testBuild := screwdriver.Build{
		ID:       9999,
		Commands: cmds,
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	testAPI.EXPECT().
		UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq("test"))
	testAPI.EXPECT().
		UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq("test"), gomock.Eq(0))

	var emitterWritten bytes.Buffer
	testEmitter := mock_screwdriver.NewMockEmitter(ctrl)
	testEmitter.EXPECT().
		Write(gomock.Any()).
		Do(emitterWritten.Write).
		AnyTimes()
	testEmitter.EXPECT().
		StartCmd(gomock.Any()).
		AnyTimes()
	testEmitter.EXPECT().
		Close().
		AnyTimes()

	wantFlattened := []string{}
	for k, v := range want {
		wantFlattened = append(wantFlattened, strings.Join([]string{k, v}, "="))
	}
	err := Run("", baseEnv, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/sh", TestBuildTimeout, envFilepath, "")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	found := map[string]string{}
	var foundCmd string

	scanner := bufio.NewScanner(&emitterWritten)
	for scanner.Scan() {
		line := scanner.Text()
		split := strings.Split(line, "=")
		if len(split) != 2 {
			// Capture that "$ env" output line
			if strings.HasPrefix(line, "$") {
				foundCmd = line
			}
			continue
		}
		found[split[0]] = split[1]
	}

	if foundCmd != "$ env" {
		t.Errorf("foundCmd = %q, want %q", foundCmd, "env")
	}

	for k, v := range want {
		if found[k] != v {
			t.Errorf("%v=%q, want %v", k, found[k], v)
		}
	}
}

func TestEmitter(t *testing.T) {
	envFilepath := "/tmp/testEmitter"
	setupTestCase(t, envFilepath)
	var commands = []screwdriver.CommandDef{
		{Cmd: "ls", Name: "name1"},
		{Cmd: "ls && ls", Name: "name2"},
	}

	testBuild := screwdriver.Build{
		ID:       9999,
		Commands: commands,
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testAPI := NewDoNothingAPI(ctrl)
	testEmitter := mock_screwdriver.NewMockEmitter(ctrl)
	testEmitter.EXPECT().
		Write(gomock.Any()).
		DoAndReturn(IgnoringWrite).
		AnyTimes()
	testEmitter.EXPECT().
		Close().
		AnyTimes()

	for _, cmd := range commands {
		testEmitter.EXPECT().
			StartCmd(NewCmdNameMatcher(cmd.Name))
	}

	err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/sh", TestBuildTimeout, envFilepath, "")

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestUserShell(t *testing.T) {
	envFilepath := "/tmp/testUserShell"
	setupTestCase(t, envFilepath)
	commands := []screwdriver.CommandDef{
		{Cmd: "if [ $0 != /bin/bash ]; then exit 1; fi", Name: "sd-setup-step"},
		{Cmd: "if [ $0 != /bin/bash ]; then exit 1; fi", Name: "user-step"},
		{Cmd: "if [ $0 != /bin/bash ]; then exit 1; fi", Name: "teardown-user-step"},
		{Cmd: "if [ $0 != /bin/bash ]; then exit 1; fi", Name: "sd-teardown-step"}, // source is not available in sh
	}
	var env []map[string]string
	env = append(env, map[string]string{
		"USER_SHELL_BIN": "/bin/bash",
	})
	testBuild := screwdriver.Build{
		ID:          12345,
		Commands:    commands,
		Environment: env,
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testAPI := NewDoNothingAPI(ctrl)
	testEmitter := NewDoNothingEmitter(ctrl)
	err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/bash", TestBuildTimeout, envFilepath, "")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}
