package handler

type exchangeRequest struct {
	DeviceType  string `json:"device_type"`
	DeviceName  string `json:"device_name"`
	LoginMethod string `json:"login_method"`
}
