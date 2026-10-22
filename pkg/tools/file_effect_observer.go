package tools

// SetFileEffectBeforeWriteHook exposes the same durable file-engine observation
// cut through the actual adapter. Restore the observer after use.
func (t *AppendFileTool) SetFileEffectBeforeWriteHook(hook func(FileEffectWitness) error) func() {
	return SetFileEffectBeforeWriteHook(hook)
}

func (t *WriteFileTool) SetFileEffectBeforeWriteHook(hook func(FileEffectWitness) error) func() {
	return SetFileEffectBeforeWriteHook(hook)
}

func (t *EditFileTool) SetFileEffectBeforeWriteHook(hook func(FileEffectWitness) error) func() {
	return SetFileEffectBeforeWriteHook(hook)
}
