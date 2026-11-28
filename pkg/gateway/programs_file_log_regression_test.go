package gateway

// FR020/I1/B5/S46/S48: program-token filtering never depends on the general
// filter, including unchanged file reads and the actual configured log sink.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramMasking_FileAndLogRegression(t *testing.T) {
	t.Run("F4_unchanged_file_read_general_filter_off", func(t *testing.T) {
		x := newProgramsSentinelFixture(t, false, func(cfg *config.Config) {
			cfg.Sandbox.ToolPolicies["read_file"] = "allow"
			cfg.Agents.List[0].Tools.Builtin.Policies["read_file"] = config.ToolPolicyAllow
		})
		require.False(t, x.al.GetConfig().Tools.FilterSensitiveData)
		require.True(t, pgSinkHasProtectedWindow([]byte(programsSinkSentinel[4:12]), programsSinkSentinel), "instrument: detector sees an interior eight-byte window")
		file := filepath.Join(x.work, "f4-unchanged.txt")
		result := x.startTurn(t, "f4-write", map[string]any{"command": fmt.Sprintf("printf 'F4_FILE_CONTROL\\n%%s\\n' \"$GH_TOKEN\" > %q; printf 'F4_WRITER_RAN'", file)})
		require.Equal(t, "F4_WRITER_RAN", result, "instrument: actual registered child wrote the diagnostic file")
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		require.True(t, bytes.Equal(raw, []byte("F4_FILE_CONTROL\n"+programsSinkSentinel+"\n")), "Given: actual file contains unchanged selected token; no transformed-secret promise claimed")
		const call = "f4-read"
		x.provider.setScript(x.dev, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: call, Type: "function", Name: "read_file", Arguments: map[string]any{"path": file}}}}, wu5StopGW("F4 file result received")})
		require.NoError(t, x.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "webchat", SessionID: x.dev, ChatID: x.dev, Content: "Read the diagnostic file", Provenance: bus.NewProvenance(bus.OriginWeb)}))
		x.waitEnds(t, x.dev)
		read := x.provider.result(x.dev, call)
		require.True(t, strings.Contains(read, "F4_FILE_CONTROL"), "instrument: real read_file result reached the model")
		assert.True(t, strings.Contains(read, "[FILTERED]"), "FR020: actual admitted file output contains replacement, not empty text")
		pgSinkAssertValueFree(t, []byte(read), "actual read_file admission/model result")
		x.shutdown()
		entries, err := x.al.GetSessionStore().ReadTranscript(x.dev)
		require.NoError(t, err)
		encoded, err := json.Marshal(entries)
		require.NoError(t, err)
		require.True(t, bytes.Contains(encoded, []byte("F4_FILE_CONTROL")), "instrument: actual file output was archived")
		pgSinkAssertValueFree(t, encoded, "actual persisted file-read transcript")
		x.provider.mu.Lock()
		prompts, marshalErr := json.Marshal(x.provider.prompts[x.dev])
		x.provider.mu.Unlock()
		require.NoError(t, marshalErr)
		require.True(t, bytes.Contains(prompts, []byte("F4_FILE_CONTROL")), "instrument: actual file output reached a recorded model request")
		pgSinkAssertValueFree(t, prompts, "actual file-read model requests")
	})
	t.Run("F4_program_values_filtered_at_native_and_slog_sink", func(t *testing.T) {
		x := newProgramsSentinelFixture(t, false)
		x.startTurn(t, "f4-log-runtime-control", map[string]any{"command": "printenv GH_TOKEN | wc -c"})
		require.False(t, x.al.GetConfig().Tools.FilterSensitiveData)
		logger.InfoCF("f4-regression", "F4_NATIVE_VALUE "+programsSinkSentinel, map[string]any{"value": programsSinkSentinel})
		slog.Info("F4_SLOG_VALUE "+programsSinkSentinel, "value", programsSinkSentinel)
		raw, err := os.ReadFile(filepath.Join(x.home, "logs", "gateway.log"))
		require.NoError(t, err)
		for _, marker := range []string{"S48_NATIVE_LOG_READER_CONTROL", "S48_SLOG_READER_CONTROL", "F4_NATIVE_VALUE", "F4_SLOG_VALUE"} {
			require.True(t, bytes.Contains(raw, []byte(marker)), "instrument: configured log reader sees %s", marker)
		}
		require.True(t, bytes.Contains(raw, []byte("[FILTERED]")), "FR020: actual log sink contains a replacement")
		pgSinkAssertValueFree(t, raw, "actual native+slog gateway file sink")
	})
}
