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

func NewDonothingEmitter(ctrl *gomock.Controller) *mock_screwdriver.MockEmitter {
	testEmitter := mock_screwdriver.NewMockEmitter(ctrl)
	testEmitter.EXPECT().
		StartCmd(gomock.Any()).
		AnyTimes()
	testEmitter.EXPECT().
		Write(gomock.Any()).
		AnyTimes()
	testEmitter.EXPECT().
		Close().
		AnyTimes()
	return testEmitter
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
				UpdateStepStop(gomock.Any(), gomock.Eq("test"), gomock.Any()).
				AnyTimes()
			testEmitter := NewDonothingEmitter(ctrl)

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
	tests := []struct {
		cmd                 screwdriver.CommandDef
		expectStopErrorCode bool
	}{
		{screwdriver.CommandDef{Cmd: "ls", Name: "test ls"}, false},
		{screwdriver.CommandDef{Cmd: "export FOO=BAR", Name: "test export env"}, false},
		{screwdriver.CommandDef{Cmd: `if [ -z "$FOO" ] ; then exit 1; fi`, Name: "test if env set"}, false},
		{screwdriver.CommandDef{Cmd: "doesnotexit", Name: "test doesnotexit err"}, true},
		{screwdriver.CommandDef{Cmd: "echo user teardown step", Name: "teardown-echo"}, false},
		{screwdriver.CommandDef{Cmd: "sleep 1", Name: "test sleep 1"}, true},
		{screwdriver.CommandDef{Cmd: "echo upload artifacts", Name: "sd-teardown-artifacts"}, false},
	}

	var commands []screwdriver.CommandDef
	for _, tt := range tests {
		commands = append(commands, tt.cmd)
	}
	testBuild := screwdriver.Build{
		ID:          12345,
		Commands:    commands,
		Environment: []map[string]string{},
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	for _, tt := range tests {
		testAPI.EXPECT().
			UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq(tt.cmd.Name))
		errorCodeMatcher := gomock.Eq(0)
		if tt.expectStopErrorCode {
			errorCodeMatcher = gomock.Not(errorCodeMatcher)
		}
		testAPI.EXPECT().
			UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq(tt.cmd.Name), errorCodeMatcher)
	}
	testEmitter := NewDonothingEmitter(ctrl)

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
		{Cmd: "doesnotexit", Name: "doesnotexit"},
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
	testAPI.EXPECT().
		UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Any())
	testAPI.EXPECT().
		UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq("preteardown-foo"), gomock.Eq(0))
	testAPI.EXPECT().
		UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq("teardown-singlequote"), gomock.Eq(0))
	testAPI.EXPECT().
		UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq("sd-teardown-doublequote"), gomock.Eq(0))
	testAPI.EXPECT().
		UpdateStepStop(gomock.Eq(testBuild.ID), gomock.Eq("doesnotexit"), gomock.Not(gomock.Eq(0)))

	testEmitter := NewDonothingEmitter(ctrl)
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
		{Cmd: "doesnotexit", Name: "sd-teardown-artifacts"},
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
	testEmitter := NewDonothingEmitter(ctrl)

	err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/sh", TestBuildTimeout, envFilepath, "")
	expectedErr := ErrStatus{127}
	if !reflect.DeepEqual(err, expectedErr) {
		t.Fatalf("Unexpected error: %v - should be %v", err, expectedErr)
	}
}

type funcMatcher struct {
	matches     func(x interface{}) bool
	description string
}

func (f *funcMatcher) Matches(x interface{}) bool {
	return f.matches(x)
}

func (f *funcMatcher) String() string {
	return f.description
}

func NewFuncMatcher(matches func(interface{}) bool, description string) *funcMatcher {
	return &funcMatcher{
		matches:     matches,
		description: description,
	}
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

	testAPI.EXPECT().
		UpdateStepStart(gomock.Any(), gomock.Any()).
		AnyTimes()
	testAPI.EXPECT().
		UpdateStepStop(gomock.Any(), gomock.Eq("completed"), gomock.Any()).
		Times(0)

	testEmitter := NewDonothingEmitter(ctrl)
	testEmitter.EXPECT().
		StartCmd(NewFuncMatcher(func(i interface{}) bool {
			return i.(screwdriver.CommandDef).Cmd == "sleep 3"
		}, `cmd.Cmd is "sleep 3"`)).
		Do(func() {
			time.Sleep(10000)
		})

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
		Close()

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

	emitterWritten.Reset()
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
	var tests = []struct {
		command string
		name    string
	}{
		{"ls", "name1"},
		{"ls && ls", "name2"},
	}

	testBuild := screwdriver.Build{
		ID:       9999,
		Commands: []screwdriver.CommandDef{},
	}
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	testAPI.EXPECT().
		UpdateStepStart(gomock.Eq(testBuild.ID), gomock.Eq("name2"))
	testEmitter := mock_screwdriver.NewMockEmitter(ctrl)
	testEmitter.EXPECT().
		Write(gomock.Any()).
		AnyTimes()
	testEmitter.EXPECT().
		Close().
		AnyTimes()

	for _, test := range tests {
		testBuild.Commands = append(testBuild.Commands, screwdriver.CommandDef{
			Name: test.name,
			Cmd:  test.command,
		})
		testEmitter.EXPECT().
			StartCmd(NewFuncMatcher(func(i interface{}) bool {
				return i.(screwdriver.CommandDef).Name == test.name
			}, fmt.Sprintf("cmd.Name == %#v", test.name)))
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

	testAPI := mock_screwdriver.NewMockAPI(ctrl)
	testEmitter := NewDonothingEmitter(ctrl)
	err := Run("", nil, testEmitter, testBuild, testAPI, testBuild.ID, "/bin/bash", TestBuildTimeout, envFilepath, "")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}
