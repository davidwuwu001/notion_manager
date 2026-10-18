package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Validate the billing state, not legacy plan_type=team (also used by Free).
func requireBusinessSubscription(acc *Account) error {
	body, err := json.Marshal(map[string]string{"spaceId": acc.SpaceID})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, NotionAPIBase+"/getFeatureBillingData", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", accountCookieHeader(acc))
	req.Header.Set("x-notion-active-user-header", acc.UserID)
	req.Header.Set("User-Agent", AppConfig.Browser.UserAgent)
	resp, err := discoveryHTTPClient(AppConfig.APITimeoutDuration()).Do(req)
	if err != nil {
		return fmt.Errorf("无法核实工作区订阅，请稍后重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("无法核实工作区订阅 (HTTP %d)", resp.StatusCode)
	}
	var data featureBillingResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return fmt.Errorf("无法解析工作区订阅，未导入")
	}
	now := time.Now()
	if err := data.requireBusiness(now); err != nil {
		return err
	}
	acc.BusinessVerifiedAt = &now
	return nil
}

type featureBillingResponse struct {
	BillingData *struct {
		Type         string          `json:"type"`
		Trial        json.RawMessage `json:"trial"`
		Subscription *struct {
			TrialEnd *string `json:"trialEnd"`
			Items    []struct {
				Price struct {
					Product string `json:"product"`
				} `json:"price"`
			} `json:"items"`
		} `json:"subscription"`
	} `json:"billingData"`
}

func (data featureBillingResponse) requireBusiness(now time.Time) error {
	b := data.BillingData
	if b == nil || (b.Type != "public" && b.Type != "admin") {
		return fmt.Errorf("无法确认工作区的正式 Business 订阅，未导入")
	}
	// A separate trial can grant Business over a Free/Plus subscription.
	if len(b.Trial) > 0 && string(b.Trial) != "null" {
		return fmt.Errorf("仅允许正式 Business Plan，Business 试用不允许导入")
	}
	if b.Subscription != nil {
		if b.Subscription.TrialEnd != nil {
			end, err := time.Parse(time.RFC3339Nano, *b.Subscription.TrialEnd)
			if err != nil || end.After(now) {
				return fmt.Errorf("仅允许正式 Business Plan，试用或状态不明的工作区不允许导入")
			}
		}
		for _, item := range b.Subscription.Items {
			if item.Price.Product == "business" {
				return nil
			}
		}
	}
	return fmt.Errorf("仅允许正式 Business Plan；此工作区不是正式 Business 订阅")
}
