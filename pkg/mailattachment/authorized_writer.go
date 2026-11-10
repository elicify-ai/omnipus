package mailattachment

// AuthorizeDestination adds the caller's filesystem-policy check to each
// destination operation. It does not grant paths or replace rooted I/O.
func AuthorizeDestination(writer DestinationWriter, authorize func(string) error) DestinationWriter {
	return authorizedDestination{DestinationWriter: writer, authorize: authorize}
}

type authorizedDestination struct {
	DestinationWriter
	authorize func(string) error
}

func (w authorizedDestination) CleanRel(raw string) (string, error) {
	rel, err := w.DestinationWriter.CleanRel(raw)
	if err != nil {
		return "", err
	}
	return rel, w.authorize(rel)
}

func (w authorizedDestination) ValidateCreateName(rel string) error {
	if err := w.DestinationWriter.ValidateCreateName(rel); err != nil {
		return err
	}
	return w.authorize(rel)
}

func (w authorizedDestination) Mkdir(rel string) error {
	if err := w.authorize(rel); err != nil {
		return err
	}
	return w.DestinationWriter.Mkdir(rel)
}

func (w authorizedDestination) CreateUnique(rel string) (string, writerFile, error) {
	if err := w.authorize(rel); err != nil {
		return "", nil, err
	}
	return w.DestinationWriter.CreateUnique(rel)
}

// Remove is service cleanup of its own exclusive reservation, including after
// a final-name policy refusal. The underlying rooted writer retains containment.
func (w authorizedDestination) Remove(rel string) error {
	if cleanup, ok := w.DestinationWriter.(remover); ok {
		return cleanup.Remove(rel)
	}
	return ErrDestinationWrite
}
