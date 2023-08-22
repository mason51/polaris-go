/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/traffic_manage/ratelimiter"

	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	// 初始化
	OperationInit = "init"
	// 上报
	OperationReport = "report"
)

// 只模拟server异常接口场景，不模拟正常场景
type MockRateLimitServer struct {
	mutex            sync.RWMutex
	operation4xx     map[string]bool
	responseNoReturn map[string]bool
	responseDelay    map[string]bool
	mockMaxAmount    int64
	clientKeys       map[string]uint32
}

// 创建mock server
func NewMockRateLimitServer() *MockRateLimitServer {
	return &MockRateLimitServer{
		operation4xx:     map[string]bool{},
		responseNoReturn: map[string]bool{},
		responseDelay:    map[string]bool{},
		clientKeys:       map[string]uint32{},
		mockMaxAmount:    200,
	}
}

// 重置
func (m *MockRateLimitServer) Reset() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.operation4xx = map[string]bool{}
	m.responseNoReturn = map[string]bool{}
	m.responseDelay = map[string]bool{}
	m.clientKeys = map[string]uint32{}
	atomic.StoreInt64(&m.mockMaxAmount, 200)
}

// 设置最大限流阈值
func (m *MockRateLimitServer) SetMockMaxAmount(v int64) {
	atomic.StoreInt64(&m.mockMaxAmount, v)
}

// 标识某个接口固定返回4XX
func (m *MockRateLimitServer) MarkOperation4XX(operation string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.operation4xx[operation] = true
}

// 标识某个接口不返回应答
func (m *MockRateLimitServer) MarkOperationNoReturn(operation string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.responseNoReturn[operation] = true
}

// 标识某个接口延迟一个周期
func (m *MockRateLimitServer) MarkOperationDelay(operation string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.responseDelay[operation] = true
}

// 设置clientKey
func (m *MockRateLimitServer) SetClientKey(uid string, key uint32) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.clientKeys[uid] = key
}

const delayDuration = 2 * time.Second

// 处理请求
func (m *MockRateLimitServer) processRequest(request *ratelimiter.RateLimitRequest) *ratelimiter.RateLimitResponse {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	switch request.Cmd {
	case ratelimiter.RateLimitCmd_INIT:
		initReq := request.GetRateLimitInitRequest()
		if m.operation4xx[OperationInit] {
			initResp := &ratelimiter.RateLimitInitResponse{
				Code:       400213,
				Target:     initReq.GetTarget(),
				ClientKey:  0,
				Counters:   nil,
				SlideCount: 0,
				Timestamp:  model.CurrentMillisecond(),
			}
			return &ratelimiter.RateLimitResponse{
				Cmd:                   ratelimiter.RateLimitCmd_INIT,
				RateLimitInitResponse: initResp,
			}
		}
		if m.responseNoReturn[OperationInit] {
			// 忽略请求，不处理
			return nil
		}
		timeMilli := model.CurrentMillisecond()
		if m.responseDelay[OperationInit] {
			// 等待一段时间，再返回
			time.Sleep(delayDuration)
		}
		initResp := &ratelimiter.RateLimitInitResponse{
			Code:      200000,
			Target:    initReq.GetTarget(),
			ClientKey: 1,
			Counters: []*ratelimiter.QuotaCounter{
				{
					Duration:    initReq.Totals[0].Duration,
					CounterKey:  m.clientKeys[initReq.ClientId],
					Left:        atomic.LoadInt64(&m.mockMaxAmount),
					Mode:        ratelimiter.Mode_BATCH_OCCUPY,
					ClientCount: uint32(len(m.clientKeys)),
				},
			},
			Timestamp: timeMilli,
		}
		return &ratelimiter.RateLimitResponse{
			Cmd:                   ratelimiter.RateLimitCmd_INIT,
			RateLimitInitResponse: initResp,
		}
	case ratelimiter.RateLimitCmd_ACQUIRE:
		reportReq := request.GetRateLimitReportRequest()
		if m.operation4xx[OperationReport] {
			reportResp := &ratelimiter.RateLimitReportResponse{
				Code:      400213,
				Timestamp: model.CurrentMillisecond(),
			}
			return &ratelimiter.RateLimitResponse{
				Cmd:                     ratelimiter.RateLimitCmd_ACQUIRE,
				RateLimitReportResponse: reportResp,
			}
		}
		if m.responseNoReturn[OperationReport] {
			// 忽略请求，不处理
			return nil
		}
		timeMilli := model.CurrentMillisecond()
		if m.responseDelay[OperationReport] {
			// 等待一段时间，再返回
			time.Sleep(delayDuration)
		}
		reportResp := &ratelimiter.RateLimitReportResponse{
			Code: 200000,
			QuotaLefts: []*ratelimiter.QuotaLeft{
				{
					CounterKey:  reportReq.QuotaUses[0].CounterKey,
					Left:        atomic.AddInt64(&m.mockMaxAmount, 0-int64(reportReq.QuotaUses[0].Used)),
					ClientCount: uint32(len(m.clientKeys)),
				},
			},
			Timestamp: timeMilli,
		}
		return &ratelimiter.RateLimitResponse{
			Cmd:                     ratelimiter.RateLimitCmd_ACQUIRE,
			RateLimitReportResponse: reportResp,
		}
	}
	return nil
}

// 消息处理接口
func (m *MockRateLimitServer) Service(stream ratelimiter.RateLimitGRPCV2_ServiceServer) error {
	for {
		request, err := stream.Recv()
		if err != nil {
			return err
		}
		resp := m.processRequest(request)
		if nil != resp {
			err = stream.Send(resp)
			if err != nil {
				return err
			}
		}
	}
}

// 时间对齐接口
func (m *MockRateLimitServer) TimeAdjust(ctx context.Context,
	adjustReq *ratelimiter.TimeAdjustRequest) (*ratelimiter.TimeAdjustResponse, error) {
	return &ratelimiter.TimeAdjustResponse{
		ServerTimestamp: model.CurrentMillisecond(),
	}, nil
}
