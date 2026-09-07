// Copyright (c) 2024 Tencent Inc.
// SPDX-License-Identifier: Apache-2.0
//

package score

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/config"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/constants"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/log"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/node"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/base/ret"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/errorcode"
	"github.com/tencentcloud/CubeSandbox/CubeMaster/pkg/scheduler/selctx"
)

const externalHTTPScoreName = "external_http_score"
const externalHTTPScoreMaxResponseBytes = 1 << 20

type externalHTTPScore struct {
	weight float64
}

type externalHTTPScoreRequest struct {
	RequestID    string                  `json:"request_id,omitempty"`
	Mode         string                  `json:"mode,omitempty"`
	InstanceType string                  `json:"instance_type,omitempty"`
	TemplateID   string                  `json:"template_id,omitempty"`
	Nodes        []externalHTTPScoreNode `json:"nodes"`
}

type externalHTTPScoreNode struct {
	NodeID              string  `json:"node_id"`
	NodeIP              string  `json:"node_ip,omitempty"`
	InstanceType        string  `json:"instance_type,omitempty"`
	MvmNum              int64   `json:"mvm_num"`
	RealTimeCreateNum   int64   `json:"real_time_create_num"`
	LocalCreateNum      int64   `json:"local_create_num"`
	CreateConcurrentNum int64   `json:"create_concurrent_num"`
	QuotaCPU            int64   `json:"quota_cpu"`
	QuotaMem            int64   `json:"quota_mem"`
	QuotaCPUUsage       int64   `json:"quota_cpu_usage"`
	QuotaMemUsage       int64   `json:"quota_mem_usage"`
	CPUUtil             float64 `json:"cpu_util"`
	MemUsage            int64   `json:"mem_usage"`
}

type externalHTTPScoreResponse struct {
	Scores map[string]float64 `json:"scores"`
}

func NewExternalHTTPScore() *externalHTTPScore {
	cfg := getExternalHTTPScoreConfig()
	if cfg == nil {
		return &externalHTTPScore{}
	}
	return &externalHTTPScore{
		weight: cfg.Weight,
	}
}

func (l *externalHTTPScore) ID() string {
	return constants.SelectorScoreID + "/" + externalHTTPScoreName
}

func (l *externalHTTPScore) String() string {
	return l.ID()
}

func (l *externalHTTPScore) Weight() float64 {
	return l.weight
}

func (l *externalHTTPScore) Disable() bool {
	cfg := getExternalHTTPScoreConfig()
	return cfg == nil || cfg.Disable
}

func (l *externalHTTPScore) Select(selCtx *selctx.SelectorCtx) (nodes node.NodeScoreList, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ret.Errorf(errorcode.ErrorCode_MasterInternalError, "externalHTTPScore panic:%s", r)
		}
	}()

	cfg := getExternalHTTPScoreConfig()
	if cfg == nil {
		return nil, nil
	}
	if l.Disable() || cfg.Endpoint == "" {
		return nil, nil
	}

	inList := selCtx.Nodes()
	if inList.Len() == 0 {
		return nil, nil
	}

	knownNodes, err := validateExternalHTTPScoreCandidates(inList)
	if err != nil {
		log.G(externalHTTPScoreContext(selCtx)).Warnf("external_http_score candidate validation failed, err: %v", err)
		return nil, err
	}

	reqBody := buildExternalHTTPScoreRequest(selCtx, cfg.Mode, inList)
	respScores, err := requestExternalHTTPScores(selCtx.Ctx, cfg.Endpoint, cfg.Timeout, reqBody)
	if err != nil {
		log.G(externalHTTPScoreContext(selCtx)).Warnf("external_http_score request failed, endpoint: %s, err: %v", cfg.Endpoint, err)
		return nil, err
	}
	if err := validateExternalHTTPScoreResponse(respScores, knownNodes); err != nil {
		log.G(externalHTTPScoreContext(selCtx)).Warnf("external_http_score response validation failed, endpoint: %s, err: %v", cfg.Endpoint, err)
		return nil, err
	}

	nodes = make(node.NodeScoreList, 0, inList.Len())
	for _, n := range inList {
		score, ok := respScores[n.ID()]
		if !ok {
			return nil, fmt.Errorf("external_http_score missing node score: %s", n.ID())
		}
		nodes.Append(&node.NodeScore{
			InsID:    n.ID(),
			Score:    score,
			MvmNum:   n.MvmNum,
			OrigNode: n,
		})
	}
	return nodes, nil
}

func validateExternalHTTPScoreResponse(scores map[string]float64, knownNodes map[string]struct{}) error {
	if scores == nil {
		return fmt.Errorf("external_http_score response scores is nil")
	}
	if len(scores) != len(knownNodes) {
		return fmt.Errorf("external_http_score score count mismatch: got %d want %d", len(scores), len(knownNodes))
	}
	for nodeID, score := range scores {
		if _, ok := knownNodes[nodeID]; !ok {
			return fmt.Errorf("external_http_score unknown node: %s", nodeID)
		}
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 100 {
			return fmt.Errorf("external_http_score invalid score for node %s: %f", nodeID, score)
		}
	}
	return nil
}

func validateExternalHTTPScoreCandidates(inList node.NodeList) (map[string]struct{}, error) {
	knownNodes := make(map[string]struct{}, inList.Len())
	for i, n := range inList {
		if n == nil {
			return nil, fmt.Errorf("external_http_score nil candidate node at index %d", i)
		}
		nodeID := n.ID()
		if nodeID == "" {
			return nil, fmt.Errorf("external_http_score empty candidate node id at index %d", i)
		}
		if _, ok := knownNodes[nodeID]; ok {
			return nil, fmt.Errorf("external_http_score duplicate candidate node: %s", nodeID)
		}
		knownNodes[nodeID] = struct{}{}
	}
	return knownNodes, nil
}

func buildExternalHTTPScoreRequest(selCtx *selctx.SelectorCtx, mode string, inList node.NodeList) externalHTTPScoreRequest {
	req := externalHTTPScoreRequest{
		Mode:         mode,
		InstanceType: selCtx.InstanceType,
		Nodes:        make([]externalHTTPScoreNode, 0, inList.Len()),
	}
	if selCtx.ReqRes != nil {
		req.TemplateID = selCtx.ReqRes.TemplateID
	}

	for _, n := range inList {
		nodeID := n.ID()
		req.Nodes = append(req.Nodes, externalHTTPScoreNode{
			NodeID:              nodeID,
			NodeIP:              n.IP,
			InstanceType:        n.InstanceType,
			MvmNum:              n.MvmNum,
			RealTimeCreateNum:   n.RealTimeCreateNum,
			LocalCreateNum:      n.LocalCreateNum,
			CreateConcurrentNum: n.CreateConcurrentNum,
			QuotaCPU:            n.QuotaCpu,
			QuotaMem:            n.QuotaMem,
			QuotaCPUUsage:       n.QuotaCpuUsage,
			QuotaMemUsage:       n.QuotaMemUsage,
			CPUUtil:             n.CpuUtil,
			MemUsage:            n.MemUsage,
		})
	}
	return req
}

func requestExternalHTTPScores(ctx context.Context, endpoint string, timeout time.Duration, reqBody externalHTTPScoreRequest) (map[string]float64, error) {
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("external_http_score unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, externalHTTPScoreMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > externalHTTPScoreMaxResponseBytes {
		return nil, fmt.Errorf("external_http_score response body exceeds %d bytes", externalHTTPScoreMaxResponseBytes)
	}

	var out externalHTTPScoreResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Scores) == 0 {
		return nil, fmt.Errorf("external_http_score response scores is empty")
	}
	return out.Scores, nil
}

func externalHTTPScoreContext(selCtx *selctx.SelectorCtx) context.Context {
	if selCtx != nil && selCtx.Ctx != nil {
		return selCtx.Ctx
	}
	return context.Background()
}

func getExternalHTTPScoreConfig() *config.ExternalHTTPScore {
	cfg := config.GetConfig()
	if cfg == nil || cfg.Scheduler == nil || cfg.Scheduler.Score == nil {
		return nil
	}
	return cfg.Scheduler.Score.ScorePluginConf.ExternalHTTPScore
}
