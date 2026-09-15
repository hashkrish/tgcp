package core

// TrackJob runs fn -- the actual mutating GCP API call -- and records the
// outcome as a Job (filling Status/Error from the error fn returns) via
// RecordJob before returning.
//
// Call this from inside the tea.Cmd closure that performs the mutation, not
// from a Bubble Tea Update() handler reacting to the result message. A
// tea.Cmd's closure always runs to completion in its own goroutine, but the
// tea.Msg it returns is only delivered to whichever service is currently
// active by the time it comes back -- if the user has switched screens or
// services in the meantime, that message (and any RecordJob call inside its
// handler) is silently dropped. Recording the job here, synchronously with
// the API call itself, means it's tracked regardless of what's on screen
// when the call completes.
func TrackJob(job Job, fn func() error) error {
	err := fn()
	if err != nil {
		job.Status = JobFailed
		job.Error = err.Error()
	} else {
		job.Status = JobSuccess
	}
	RecordJob(job)
	return err
}
