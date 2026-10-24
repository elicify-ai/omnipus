package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestHistoryRepairCommand_ActualSharedPrepareIsReadOnly(t *testing.T) {
	root := repairCanonicalFixture(t)
	dirs, err := os.ReadDir(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	require.Len(t, dirs, 1)
	dir := filepath.Join(root, "sessions", dirs[0].Name())
	before := repairTree(t, root)
	build, err := session.PrepareChatHistoryIndex(context.Background(), dir, session.ChatHistoryIndexBuildOptions{})
	require.NoError(t, err, "real shared offline constructor must prepare, not return not-implemented")
	require.NotNil(t, build)
	require.EqualValues(t, 40, build.Summary().SourceGroupCount)
	require.Equal(t, 1, build.Summary().PartitionCount)
	require.Equal(t, before, repairTree(t, root), "prepare and summary write no source/index/temp/lock/log bytes, modes or mtimes")
	require.NoError(t, build.Close())
	require.Equal(t, before, repairTree(t, root))
}
