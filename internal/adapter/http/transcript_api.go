package httpadapter

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

const transcriptSegmentBodyLimitBytes int64 = 64 * 1024

type TranscriptIngestUseCases interface {
	StoreTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error)
}

type TranscriptAPI struct {
	service TranscriptIngestUseCases
	apiKey  string
}

func NewTranscriptAPI(service TranscriptIngestUseCases, apiKey string) *TranscriptAPI {
	return &TranscriptAPI{service: service, apiKey: apiKey}
}

func (api *TranscriptAPI) Store(w http.ResponseWriter, r *http.Request) {
	if !api.authorized(r.Header.Get("X-DeciScope-Api-Key")) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
		return
	}

	var request transcriptSegmentRequest
	r.Body = http.MaxBytesReader(w, r.Body, transcriptSegmentBodyLimitBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		if isBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if isBodyTooLarge(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request body")
		return
	}

	segment, err := request.toDomain()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	result, err := api.service.StoreTranscriptSegment(r.Context(), segment)
	if errors.Is(err, domain.ErrConflict) {
		log.Printf("Transcript segment conflict. eventId=%s callId=%s sequenceNo=%d", segment.EventID, segment.CallID, segment.SequenceNo)
		writeError(w, http.StatusConflict, "conflict", "transcript segment conflict")
		return
	}
	if err != nil {
		log.Printf("Store transcript segment failed. eventId=%s callId=%s sequenceNo=%d error=%v", segment.EventID, segment.CallID, segment.SequenceNo, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}

	switch result.Status {
	case domain.TranscriptSegmentCreated:
		log.Printf("Transcript segment stored. eventId=%s callId=%s sequenceNo=%d", segment.EventID, segment.CallID, segment.SequenceNo)
		writeJSON(w, http.StatusCreated, transcriptSegmentResponse{Status: string(result.Status), Duplicate: false, EventID: segment.EventID})
	case domain.TranscriptSegmentAlreadyExists:
		log.Printf("Duplicate transcript segment ignored. eventId=%s callId=%s sequenceNo=%d", segment.EventID, segment.CallID, segment.SequenceNo)
		writeJSON(w, http.StatusOK, transcriptSegmentResponse{Status: string(result.Status), Duplicate: true, EventID: segment.EventID})
	default:
		log.Printf("Store transcript segment returned unknown status. eventId=%s callId=%s sequenceNo=%d", segment.EventID, segment.CallID, segment.SequenceNo)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func (api *TranscriptAPI) authorized(value string) bool {
	if value == "" || api.apiKey == "" {
		return false
	}
	got := sha256.Sum256([]byte(value))
	want := sha256.Sum256([]byte(api.apiKey))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

type transcriptSegmentRequest struct {
	EventID         string `json:"eventId"`
	CallID          string `json:"callId"`
	SequenceNo      int64  `json:"sequenceNo"`
	RecognizedAtUTC string `json:"recognizedAtUtc"`
	OffsetTicks     int64  `json:"offsetTicks"`
	DurationTicks   int64  `json:"durationTicks"`
	Text            string `json:"text"`
}

type transcriptSegmentResponse struct {
	Status    string `json:"status"`
	Duplicate bool   `json:"duplicate"`
	EventID   string `json:"eventId"`
}

func (request transcriptSegmentRequest) toDomain() (domain.TranscriptSegment, error) {
	eventID := strings.TrimSpace(request.EventID)
	if eventID == "" {
		return domain.TranscriptSegment{}, fmt.Errorf("eventId is required")
	}
	callID := strings.TrimSpace(request.CallID)
	if callID == "" {
		return domain.TranscriptSegment{}, fmt.Errorf("callId is required")
	}
	if request.SequenceNo < 1 {
		return domain.TranscriptSegment{}, fmt.Errorf("sequenceNo must be 1 or greater")
	}
	recognizedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.RecognizedAtUTC))
	if err != nil {
		return domain.TranscriptSegment{}, fmt.Errorf("recognizedAtUtc must be RFC3339")
	}
	if _, offset := recognizedAt.Zone(); offset != 0 {
		return domain.TranscriptSegment{}, fmt.Errorf("recognizedAtUtc must use UTC offset")
	}
	if request.OffsetTicks < 0 {
		return domain.TranscriptSegment{}, fmt.Errorf("offsetTicks must be 0 or greater")
	}
	if request.DurationTicks < 0 {
		return domain.TranscriptSegment{}, fmt.Errorf("durationTicks must be 0 or greater")
	}
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return domain.TranscriptSegment{}, fmt.Errorf("text is required")
	}
	return domain.TranscriptSegment{
		EventID:         eventID,
		CallID:          callID,
		SequenceNo:      request.SequenceNo,
		RecognizedAtUTC: recognizedAt.UTC(),
		OffsetTicks:     request.OffsetTicks,
		DurationTicks:   request.DurationTicks,
		Text:            text,
	}, nil
}

func isJSONContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func isBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}
