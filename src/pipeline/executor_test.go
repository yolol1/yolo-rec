package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// testStage 可配置失败行为的测试阶段
type testStage struct {
	name    string
	fail    bool
	outputs []FileInfo
	called  *int
}

func (s *testStage) Name() string { return s.name }

func (s *testStage) Execute(ctx *PipelineContext, input []FileInfo) ([]FileInfo, error) {
	*s.called++
	if s.fail {
		return input, errors.New(s.name + " failed")
	}
	return s.outputs, nil
}

func TestExecuteContinueOnFailure(t *testing.T) {
	e := NewExecutor(logrus.New())
	stage1Called := 0
	stage2Called := 0
	e.RegisterStage("test_fail", func(c StageConfig) (Stage, error) {
		return &testStage{name: "test_fail", fail: true, called: &stage1Called}, nil
	})
	e.RegisterStage("test_ok", func(c StageConfig) (Stage, error) {
		return &testStage{name: "test_ok", outputs: []FileInfo{{Path: "out.txt"}}, called: &stage2Called}, nil
	})

	config := &PipelineConfig{Stages: []StageConfig{
		{Name: "test_fail", ContinueOnFailure: EnabledPtr(true)},
		{Name: "test_ok"},
	}}
	results, err := e.Execute(&PipelineContext{Ctx: context.Background()}, config, nil, nil)

	// 任务最终仍标记失败（聚合错误）
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "test_fail failed")
	// 后续阶段照常执行
	assert.Equal(t, 1, stage1Called)
	assert.Equal(t, 1, stage2Called)
	// 阶段状态正确记录
	assert.Equal(t, StageStatusFailed, results[0].Status)
	assert.Equal(t, StageStatusCompleted, results[1].Status)
}

func TestExecuteStopOnFailureDefault(t *testing.T) {
	e := NewExecutor(logrus.New())
	stage1Called := 0
	stage2Called := 0
	e.RegisterStage("test_fail", func(c StageConfig) (Stage, error) {
		return &testStage{name: "test_fail", fail: true, called: &stage1Called}, nil
	})
	e.RegisterStage("test_ok", func(c StageConfig) (Stage, error) {
		return &testStage{name: "test_ok", called: &stage2Called}, nil
	})

	config := &PipelineConfig{Stages: []StageConfig{
		{Name: "test_fail"},
		{Name: "test_ok"},
	}}
	_, err := e.Execute(&PipelineContext{Ctx: context.Background()}, config, nil, nil)

	// 默认行为：失败即停止，后续阶段不执行
	assert.Error(t, err)
	assert.Equal(t, 1, stage1Called)
	assert.Equal(t, 0, stage2Called)
}
