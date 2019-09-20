// Code generated by MockGen. DO NOT EDIT.
// Source: emitter.go

// Package mock_screwdriver is a generated GoMock package.
package mock_screwdriver

import (
	gomock "github.com/golang/mock/gomock"
	screwdriver "github.com/screwdriver-cd/launcher/screwdriver"
	reflect "reflect"
)

// MockEmitter is a mock of Emitter interface
type MockEmitter struct {
	ctrl     *gomock.Controller
	recorder *MockEmitterMockRecorder
}

// MockEmitterMockRecorder is the mock recorder for MockEmitter
type MockEmitterMockRecorder struct {
	mock *MockEmitter
}

// NewMockEmitter creates a new mock instance
func NewMockEmitter(ctrl *gomock.Controller) *MockEmitter {
	mock := &MockEmitter{ctrl: ctrl}
	mock.recorder = &MockEmitterMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockEmitter) EXPECT() *MockEmitterMockRecorder {
	return m.recorder
}

// StartCmd mocks base method
func (m *MockEmitter) StartCmd(cmd screwdriver.CommandDef) {
	m.ctrl.T.Helper()
	m.ctrl.Call(m, "StartCmd", cmd)
}

// StartCmd indicates an expected call of StartCmd
func (mr *MockEmitterMockRecorder) StartCmd(cmd interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "StartCmd", reflect.TypeOf((*MockEmitter)(nil).StartCmd), cmd)
}

// Write mocks base method
func (m *MockEmitter) Write(p []byte) (int, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Write", p)
	ret0, _ := ret[0].(int)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Write indicates an expected call of Write
func (mr *MockEmitterMockRecorder) Write(p interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Write", reflect.TypeOf((*MockEmitter)(nil).Write), p)
}

// Close mocks base method
func (m *MockEmitter) Close() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Close")
	ret0, _ := ret[0].(error)
	return ret0
}

// Close indicates an expected call of Close
func (mr *MockEmitterMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockEmitter)(nil).Close))
}

// Error mocks base method
func (m *MockEmitter) Error() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Error")
	ret0, _ := ret[0].(error)
	return ret0
}

// Error indicates an expected call of Error
func (mr *MockEmitterMockRecorder) Error() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Error", reflect.TypeOf((*MockEmitter)(nil).Error))
}
