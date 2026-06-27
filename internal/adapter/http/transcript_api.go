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
	"strconv"
	"strings"
	"time"

	"deciscope-core-api/internal/domain"
)

const transcriptSegmentBodyLimitBytes int64 = 64 * 1024

type TranscriptIngestUseCases interface {
	StoreTranscriptSegment(ctx context.Context, segment domain.TranscriptSegment) (domain.TranscriptSegmentStoreResult, error)
	ListTranscriptSegments(ctx context.Context, callID, sessionID string, limit int) ([]domain.TranscriptSegment, error)
}

type TranscriptAPI struct {
	service     TranscriptIngestUseCases
	apiKey      string
	clientToken string
}

func NewTranscriptAPI(service TranscriptIngestUseCases, apiKey string, clientToken ...string) *TranscriptAPI {
	var token string
	if len(clientToken) > 0 {
		token = clientToken[0]
	}
	return &TranscriptAPI{service: service, apiKey: apiKey, clientToken: token}
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

func (api *TranscriptAPI) List(w http.ResponseWriter, r *http.Request) {
	if !api.authorizedClient(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	limit, err := parseTranscriptLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	callID := strings.TrimSpace(r.URL.Query().Get("callId"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	segments, err := api.service.ListTranscriptSegments(r.Context(), callID, sessionID, limit)
	if err != nil {
		log.Printf("List transcript segments failed. callId=%s sessionId=%s limit=%d error=%v", callID, sessionID, limit, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, transcriptSegmentListResponse{Items: transcriptSegmentItems(segments)})
}

func (api *TranscriptAPI) authorized(value string) bool {
	return authorizedSecret(value, api.apiKey)
}

func (api *TranscriptAPI) authorizedClient(r *http.Request) bool {
	if strings.TrimSpace(api.clientToken) == "" {
		return true
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = bearerToken(r.Header.Get("Authorization"))
	}
	return authorizedSecret(token, api.clientToken)
}

func authorizedSecret(value, secret string) bool {
	if value == "" || secret == "" {
		return false
	}
	got := sha256.Sum256([]byte(value))
	want := sha256.Sum256([]byte(secret))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

type transcriptSegmentRequest struct {
	SessionID       string `json:"sessionId"`
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

type transcriptSegmentListResponse struct {
	Items []transcriptSegmentItem `json:"items"`
}

type transcriptSegmentItem struct {
	SessionID       string `json:"sessionId,omitempty"`
	EventID         string `json:"eventId"`
	CallID          string `json:"callId"`
	SequenceNo      int64  `json:"sequenceNo"`
	RecognizedAtUTC string `json:"recognizedAtUtc"`
	OffsetTicks     int64  `json:"offsetTicks"`
	DurationTicks   int64  `json:"durationTicks"`
	Text            string `json:"text"`
	ReceivedAtUTC   string `json:"receivedAtUtc"`
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
		SessionID:       strings.TrimSpace(request.SessionID),
		EventID:         eventID,
		CallID:          callID,
		SequenceNo:      request.SequenceNo,
		RecognizedAtUTC: recognizedAt.UTC(),
		OffsetTicks:     request.OffsetTicks,
		DurationTicks:   request.DurationTicks,
		Text:            text,
	}, nil
}

func parseTranscriptLimit(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 {
		return 0, fmt.Errorf("limit must be 1 or greater")
	}
	if limit > 500 {
		return 500, nil
	}
	return limit, nil
}

func bearerToken(value string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}

func transcriptSegmentItems(segments []domain.TranscriptSegment) []transcriptSegmentItem {
	items := make([]transcriptSegmentItem, 0, len(segments))
	for _, segment := range segments {
		items = append(items, transcriptSegmentItem{
			SessionID:       segment.SessionID,
			EventID:         segment.EventID,
			CallID:          segment.CallID,
			SequenceNo:      segment.SequenceNo,
			RecognizedAtUTC: segment.RecognizedAtUTC.UTC().Format(time.RFC3339Nano),
			OffsetTicks:     segment.OffsetTicks,
			DurationTicks:   segment.DurationTicks,
			Text:            segment.Text,
			ReceivedAtUTC:   segment.ReceivedAtUTC.UTC().Format(time.RFC3339Nano),
		})
	}
	return items
}

func isJSONContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func isBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}
