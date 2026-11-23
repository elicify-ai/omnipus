package gateway

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomProgram_RESTCollectionLimit(t *testing.T) {
	f := pgPersonAPI(t)
	initial := len(pgProgramsList(t, f))
	require.LessOrEqual(t, initial, 32)
	for n := initial; n < 32; n++ {
		pgCreateProgramEntry(t, f, fmt.Sprintf(`{"display_name":"Entry %02d","program_words":["word%d"],"token_variables":["TOKEN_%d"],"network_sites":["host%d.example"]}`, n, n, n, n))
	}
	before := pgProgramsList(t, f)
	require.Len(t, before, 32, "otherwise-valid distinct entries fill, not exceed, the cap")
	w := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs", `{"display_name":"Acme","program_words":["acme"],"token_variables":["ACME_TOKEN"],"network_sites":["api.acme.example"]}`)
	require.Equal(t, http.StatusConflict, w.Code, "D-2 row 31 is collection conflict, not a field error: %s", w.Body.String())
	require.Equal(t, before, pgProgramsList(t, f))
}
