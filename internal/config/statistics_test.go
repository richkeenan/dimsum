package config_test

import (
	"testing"

	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatisticsDefaultsAndEdits(t *testing.T) {
	d, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	assert.Equal(t, config.DefaultStatistics(), d.Config().Statistics)
	d, err = config.Parse([]byte(sample + "statistics:\n  detail_days: 7 # history\n"))
	require.NoError(t, err)
	next, err := d.Edit([]config.Edit{{Path: []string{"statistics", "detail_days"}, Value: 14}})
	require.NoError(t, err)
	assert.Equal(t, 14, next.Config().Statistics.DetailDays)
	assert.Equal(t, 7, next.Config().Statistics.MinuteDays)
	for _, value := range []int{0, -1, 3651} {
		_, err := d.Edit([]config.Edit{{Path: []string{"statistics", "detail_days"}, Value: value}})
		assert.ErrorContains(t, err, "statistics.detail_days")
	}
}
