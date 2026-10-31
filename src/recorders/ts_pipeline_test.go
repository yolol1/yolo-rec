package recorders

import (
	"testing"

	"github.com/bililive-go/bililive-go/src/pipeline"
	"github.com/stretchr/testify/assert"
)

func TestAddTSRepairStageForTSOutput(t *testing.T) {
	config := &pipeline.PipelineConfig{
		Stages: []pipeline.StageConfig{{Name: pipeline.StageNameFixFlv}},
	}

	addTSRepairStage(config, []string{"recording.ts"}, false)

	assert.Len(t, config.Stages, 2)
	assert.Equal(t, pipeline.StageNameFixFlv, config.Stages[0].Name)
	assert.Equal(t, pipeline.StageNameRepairTs, config.Stages[1].Name)
	assert.True(t, config.Stages[1].ShouldContinueOnFailure())
}

func TestAddTSRepairStageAfterConvertTs(t *testing.T) {
	config := &pipeline.PipelineConfig{
		Stages: []pipeline.StageConfig{
			{Name: pipeline.StageNameFixFlv},
			{Name: pipeline.StageNameConvertTs},
			{Name: pipeline.StageNameCloudUpload},
		},
	}

	addTSRepairStage(config, []string{"recording.flv"}, true)

	assert.Len(t, config.Stages, 4)
	assert.Equal(t, pipeline.StageNameConvertTs, config.Stages[1].Name)
	assert.Equal(t, pipeline.StageNameRepairTs, config.Stages[2].Name)
	assert.Equal(t, pipeline.StageNameCloudUpload, config.Stages[3].Name)
}

func TestAddTSRepairStageSkipsFLVWithoutTSConversion(t *testing.T) {
	config := &pipeline.PipelineConfig{
		Stages: []pipeline.StageConfig{{Name: pipeline.StageNameFixFlv}},
	}

	addTSRepairStage(config, []string{"recording.flv"}, false)

	assert.Len(t, config.Stages, 1)
	assert.Equal(t, pipeline.StageNameFixFlv, config.Stages[0].Name)
}

func TestAddTSRepairStageDoesNotDuplicate(t *testing.T) {
	config := &pipeline.PipelineConfig{
		Stages: []pipeline.StageConfig{
			{Name: pipeline.StageNameRepairTs},
			{Name: pipeline.StageNameCloudUpload},
		},
	}

	addTSRepairStage(config, []string{"recording.ts"}, false)

	assert.Len(t, config.Stages, 2)
	assert.Equal(t, pipeline.StageNameRepairTs, config.Stages[0].Name)
}
