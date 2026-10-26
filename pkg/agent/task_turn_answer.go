package agent

// answerText is the conversation's byte-preserving concatenation. Its model
// view follows the same appends but excludes framework-origin content.
// Continuation fragments and completed logical messages use this same assembly.
type answerText struct {
	content      string
	modelContent string
}

func (a *answerText) append(content string, fromModel bool) string {
	a.content += content
	if fromModel {
		a.modelContent += content
	}
	return a.content
}

// markTaskModelResponse carries positive provider provenance into the existing
// continuation assembler. It records no response fragments or retry state.
func (ts *turnState) markTaskModelResponse(fromModel bool) {
	if ts.opts.RunningTaskID == "" {
		return
	}
	ts.mu.Lock()
	ts.taskModelResponse = fromModel
	ts.mu.Unlock()
}

// recordTaskModelAnswer accepts a logical model answer only after its existing
// conversation assembly/write boundary. Callers pass model-origin bytes, never
// framework notices, reasoning, tool results or goal-claim evidence.
func (ts *turnState) recordTaskModelAnswer(content string) {
	if ts.opts.RunningTaskID == "" || content == "" {
		return
	}
	ts.mu.Lock()
	ts.taskModelAnswer.append(content, true)
	ts.mu.Unlock()
}

func (ts *turnState) taskModelAnswerContent() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.taskModelAnswer.modelContent
}

func (ts *turnState) continuationModelContent() string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.continuationAccum.modelContent
}

// appendToAccumulatorFrom is the conversation's normal append with explicit
// provenance when a final reply can be replaced by a framework notice.
func (ts *turnState) appendToAccumulatorFrom(content string, fromModel bool) string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.continuationAccum.append(content, fromModel && ts.opts.RunningTaskID != "")
}
