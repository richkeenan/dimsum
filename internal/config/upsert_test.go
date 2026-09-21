package config_test

import (
	"github.com/richkeenan/dimsum/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUpsertMissingDefaultsPreservesSource(t *testing.T) {
	d, err := config.Parse([]byte(sample))
	require.NoError(t, err)
	next, err := d.Upsert([]config.Edit{{Path: []string{"statistics", "detail_days"}, Value: 14}, {Path: []string{"statistics", "hour_days"}, Value: 120}, {Path: []string{"cache", "stale_mode"}, Value: "off"}})
	require.NoError(t, err)
	assert.Equal(t, 14, next.Config().Statistics.DetailDays)
	assert.Equal(t, 120, next.Config().Statistics.HourDays)
	assert.Equal(t, "off", next.Config().Cache.StaleMode)
	assert.Contains(t, string(next.Bytes()), sample)
	assert.Equal(t, sample, string(d.Bytes()))
	_, err = d.Upsert([]config.Edit{{Path: []string{"statistics", "detail_days"}, Value: 0}})
	assert.Error(t, err)
}
