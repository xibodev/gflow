package models

// RecaptchaContext represents the reCAPTCHA token payload sent to Google Flow.
type RecaptchaContext struct {
	Token           string `json:"token"`
	ApplicationType string `json:"applicationType"`
}

// ClientContext is included in all Google Flow request payloads.
type ClientContext struct {
	ProjectID        string            `json:"projectId"`
	Tool             string            `json:"tool"`
	UserPaygateTier  string            `json:"userPaygateTier"`
	SessionID        string            `json:"sessionId"`
	RecaptchaContext *RecaptchaContext `json:"recaptchaContext,omitempty"`
}

// MediaGenerationContext holds generation metadata like batchId.
type MediaGenerationContext struct {
	BatchID                string `json:"batchId"`
	AudioFailurePreference string `json:"audioFailurePreference,omitempty"`
}

// TextPart represents text in a structured prompt.
type TextPart struct {
	Text string `json:"text"`
}

// StructuredPrompt holds prompt parts.
type StructuredPrompt struct {
	Parts []TextPart `json:"parts"`
}

// ImageInput represents a reference image input.
type ImageInput struct {
	Name           string `json:"name"`
	ImageInputType string `json:"imageInputType"`
}

// ImageRequestItem is a single image generation request in the batch.
type ImageRequestItem struct {
	ClientContext    *ClientContext    `json:"clientContext,omitempty"`
	ImageModelName   string            `json:"imageModelName"`
	ImageAspectRatio string            `json:"imageAspectRatio"`
	StructuredPrompt *StructuredPrompt `json:"structuredPrompt"`
	Seed             int64             `json:"seed"`
	ImageInputs      []ImageInput      `json:"imageInputs,omitempty"`
}

// BatchGenerateImagesRequest is the payload sent to flowMedia:batchGenerateImages.
type BatchGenerateImagesRequest struct {
	ClientContext          *ClientContext          `json:"clientContext"`
	MediaGenerationContext *MediaGenerationContext `json:"mediaGenerationContext,omitempty"`
	UseNewMedia            bool                    `json:"useNewMedia"`
	Requests               []ImageRequestItem      `json:"requests"`
}

// GeneratedImageData holds the image URL or base64 data.
type GeneratedImageData struct {
	FifeURL      string `json:"fifeUrl,omitempty"`
	ImageURI     string `json:"imageUri,omitempty"`
	EncodedImage string `json:"encodedImage,omitempty"`
}

// ImageMediaItem represents an image entry in the response.
type ImageMediaItem struct {
	Name  string `json:"name"` // mediaId
	Image struct {
		GeneratedImage GeneratedImageData `json:"generatedImage"`
	} `json:"image"`
}

// BatchGenerateImagesResponse represents the response from batchGenerateImages.
type BatchGenerateImagesResponse struct {
	Media            []ImageMediaItem `json:"media"`
	RemainingCredits any              `json:"remainingCredits,omitempty"`
}

// VideoTextInput holds the structured text prompt for video.
type VideoTextInput struct {
	StructuredPrompt *StructuredPrompt `json:"structuredPrompt"`
}

// VideoMediaRef holds a reference to a media item.
type VideoMediaRef struct {
	MediaID string `json:"mediaId"`
}

// VideoReferenceImage holds a reference image for video generation.
type VideoReferenceImage struct {
	MediaID        string `json:"mediaId"`
	ImageUsageType string `json:"imageUsageType"`
}

// VideoRequestItem is a single video generation request item.
type VideoRequestItem struct {
	AspectRatio     string                `json:"aspectRatio"`
	Seed            int64                 `json:"seed"`
	TextInput       *VideoTextInput       `json:"textInput,omitempty"`
	VideoModelKey   string                `json:"videoModelKey"`
	Metadata        map[string]any        `json:"metadata"`
	StartImage      *VideoMediaRef        `json:"startImage,omitempty"`
	EndImage        *VideoMediaRef        `json:"endImage,omitempty"`
	ReferenceImages []VideoReferenceImage `json:"referenceImages,omitempty"`
	VideoInput      *VideoMediaRef        `json:"videoInput,omitempty"`
	Resolution      string                `json:"resolution,omitempty"`
}

// BatchGenerateVideoRequest is the payload for batchAsyncGenerateVideoText / I2V / FL / etc.
type BatchGenerateVideoRequest struct {
	MediaGenerationContext *MediaGenerationContext `json:"mediaGenerationContext"`
	ClientContext          *ClientContext          `json:"clientContext"`
	Requests               []VideoRequestItem      `json:"requests"`
	UseV2ModelConfig       bool                    `json:"useV2ModelConfig,omitempty"`
}

// VideoOperationResponse holds media identifiers returned upon video job submission.
type VideoOperationResponse struct {
	Media []struct {
		Name string `json:"name"`
	} `json:"media"`
	Operations []struct {
		Operation struct {
			Name string `json:"name"`
		} `json:"operation"`
	} `json:"operations"`
	RemainingCredits any `json:"remainingCredits,omitempty"`
}

// VideoStatusCheckItem is a media item in the status check request.
type VideoStatusCheckItem struct {
	Name      string `json:"name"`
	ProjectID string `json:"projectId"`
}

// VideoStatusCheckRequest is sent to batchCheckAsyncVideoGenerationStatus.
type VideoStatusCheckRequest struct {
	Media []VideoStatusCheckItem `json:"media"`
}

// VideoStatusCheckResponse is returned by batchCheckAsyncVideoGenerationStatus.
type VideoStatusCheckResponse struct {
	Media []struct {
		Name          string `json:"name"`
		MediaMetadata struct {
			MediaStatus struct {
				MediaGenerationStatus string `json:"mediaGenerationStatus"`
				FailureReason         string `json:"failureReason,omitempty"`
				ErrorMessage          string `json:"errorMessage,omitempty"`
			} `json:"mediaStatus"`
		} `json:"mediaMetadata"`
	} `json:"media"`
}

// FlowMediaResponse represents the detailed media lookup from GET /v1/flowMedia/{name}.
type FlowMediaResponse struct {
	Name  string `json:"name"`
	Video struct {
		GeneratedVideo struct {
			FifeURL      string `json:"fifeUrl"`
			EncodedVideo string `json:"encodedVideo,omitempty"`
		} `json:"generatedVideo"`
	} `json:"video"`
	Image struct {
		GeneratedImage struct {
			FifeURL      string `json:"fifeUrl"`
			EncodedImage string `json:"encodedImage,omitempty"`
		} `json:"generatedImage"`
	} `json:"image"`
}

// UploadImageRequest is sent to /v1/flow/uploadImage.
type UploadImageRequest struct {
	ClientContext struct {
		Tool      string `json:"tool"`
		ProjectID string `json:"projectId"`
	} `json:"clientContext"`
	ImageBytes string `json:"imageBytes"`
}

// UploadImageResponse is returned by /v1/flow/uploadImage.
type UploadImageResponse struct {
	MediaID string `json:"mediaId,omitempty"`
	Name    string `json:"name,omitempty"`
	Media   *struct {
		Name    string `json:"name,omitempty"`
		MediaID string `json:"mediaId,omitempty"`
	} `json:"media,omitempty"`
}

// Asset represents a saved or downloaded generated artifact.
type Asset struct {
	ID        string `json:"id"`
	Type      string `json:"type"` // "image" or "video"
	URL       string `json:"url,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Size      int64  `json:"size,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
}

// --- Bridge Protocol Models (between Chrome Extension and Go Server) ---

// ExtensionHello is sent by the extension via POST /api/ext/hello.
type ExtensionHello struct {
	Type             string `json:"type"`
	SessionID        string `json:"session_id"`
	ClientID         string `json:"clientId"`
	FlowKey          string `json:"flowKey"`
	FlowKeyPresent   bool   `json:"flowKeyPresent"`
	ExtensionVersion string `json:"extension_version"`
}

// ExtensionHelloResponse is returned by POST /api/ext/hello.
type ExtensionHelloResponse struct {
	OK             bool   `json:"ok"`
	SessionID      string `json:"session_id"`
	Secret         string `json:"secret"`
	CallbackURL    string `json:"callback_url"`
	PollURL        string `json:"poll_url"`
	PollIntervalMs int    `json:"poll_interval_ms"`
}

// ExtensionCommand is a command sent from Go server to Chrome Extension.
type ExtensionCommand struct {
	ID     string         `json:"id"`
	Method string         `json:"method"` // "api_request", "get_media_url", "solve_captcha", "trpc_request"
	Params map[string]any `json:"params"`
}

// ExtensionPollResponse is returned by GET /api/ext/poll.
type ExtensionPollResponse struct {
	OK         bool               `json:"ok"`
	Commands   []ExtensionCommand `json:"commands"`
	ServerTime int64              `json:"server_time"`
}

// ExtensionCallback is sent by the extension via POST /api/ext/callback.
type ExtensionCallback struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id,omitempty"`
	Type      string         `json:"type,omitempty"`
	Status    int            `json:"status"`
	Data      any            `json:"data,omitempty"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	FlowKey   string         `json:"flowKey,omitempty"`
}

// APIRequestParams are parameters passed inside an ExtensionCommand for api_request.
type APIRequestParams struct {
	URL           string            `json:"url"`
	Method        string            `json:"method"`
	Headers       map[string]string `json:"headers,omitempty"`
	Body          any               `json:"body,omitempty"`
	CaptchaAction string            `json:"captchaAction,omitempty"`
}
