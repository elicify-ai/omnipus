package gateway

// NOTE: the former TestSetChannelEnabled_Cap1Duplicate422 was removed with
// ADR-029 — the one-instance-per-type cap is LIFTED (N instances per type are
// now allowed and are the whole point of the feature). Instance-id UNIQUENESS
// (a specific "<type>.<slug>" cannot be created twice) is covered by
// TestCreateChannelInstance_Duplicate_Returns409; multi-instance activation by
// TestInitChannels_NInstancesPerType / TestValidateChannels_MultipleInstancesPerTypeAllowed.
