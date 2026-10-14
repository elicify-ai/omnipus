package generated

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
)

// WU-24 CHECK F3: removing the other sign-in variant did not retire the
// device-code shape's closed fields or its single allowed method.
func TestContract_SignInStartResponseDeviceCode_UnknownFieldsRejected(t *testing.T) {
	valid, err := json.Marshal(deviceCodeFixture())
	require.NoError(t, err)
	require.NoError(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseDeviceCode", valid),
		"positive control: the unchanged device-code fields must be valid")
	for _, field := range []string{"command", "unexpected_field"} {
		t.Run(field, func(t *testing.T) {
			var payload map[string]any
			require.NoError(t, json.Unmarshal(valid, &payload))
			payload[field] = "not part of a device-code response"
			raw, err := json.Marshal(payload)
			require.NoError(t, err)
			err = validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseDeviceCode", raw)
			var validation *jsonschema.ValidationError
			require.ErrorAs(t, err, &validation,
				"additionalProperties false must reject the sole extra field %q", field)
		})
	}
}

func TestContract_SignInStartResponseDeviceCode_WrongDiscriminatorRejected(t *testing.T) {
	valid, err := json.Marshal(deviceCodeFixture())
	require.NoError(t, err)
	require.NoError(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseDeviceCode", valid),
		"positive control: device_code is the valid discriminator")
	var payload map[string]any
	require.NoError(t, json.Unmarshal(valid, &payload))
	payload["method"] = "cli_" + "login"
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	err = validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseDeviceCode", raw)
	var validation *jsonschema.ValidationError
	require.ErrorAs(t, err, &validation,
		"all other fields are valid: the wrong method alone must be rejected")
}
