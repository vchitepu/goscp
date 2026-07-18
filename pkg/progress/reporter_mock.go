package progress

import (
	"github.com/stretchr/testify/mock"
)

// MockProgressReporter is a testify/mock implementation of ProgressReporter.
type MockProgressReporter struct {
	mock.Mock
}

func (m *MockProgressReporter) Add(f FileInfo) {
	m.Called(f)
}

func (m *MockProgressReporter) Update(r Result) {
	m.Called(r)
}

func (m *MockProgressReporter) Complete() {
	m.Called()
}

func (m *MockProgressReporter) Fail(f FileInfo, err error) {
	m.Called(f, err)
}
