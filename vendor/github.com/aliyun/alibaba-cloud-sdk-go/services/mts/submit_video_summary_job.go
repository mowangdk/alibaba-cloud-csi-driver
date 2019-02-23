package mts

//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.
//
// Code generated by Alibaba Cloud SDK Code Generator.
// Changes may cause incorrect behavior and will be lost if the code is regenerated.

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/responses"
)

// SubmitVideoSummaryJob invokes the mts.SubmitVideoSummaryJob API synchronously
// api document: https://help.aliyun.com/api/mts/submitvideosummaryjob.html
func (client *Client) SubmitVideoSummaryJob(request *SubmitVideoSummaryJobRequest) (response *SubmitVideoSummaryJobResponse, err error) {
	response = CreateSubmitVideoSummaryJobResponse()
	err = client.DoAction(request, response)
	return
}

// SubmitVideoSummaryJobWithChan invokes the mts.SubmitVideoSummaryJob API asynchronously
// api document: https://help.aliyun.com/api/mts/submitvideosummaryjob.html
// asynchronous document: https://help.aliyun.com/document_detail/66220.html
func (client *Client) SubmitVideoSummaryJobWithChan(request *SubmitVideoSummaryJobRequest) (<-chan *SubmitVideoSummaryJobResponse, <-chan error) {
	responseChan := make(chan *SubmitVideoSummaryJobResponse, 1)
	errChan := make(chan error, 1)
	err := client.AddAsyncTask(func() {
		defer close(responseChan)
		defer close(errChan)
		response, err := client.SubmitVideoSummaryJob(request)
		if err != nil {
			errChan <- err
		} else {
			responseChan <- response
		}
	})
	if err != nil {
		errChan <- err
		close(responseChan)
		close(errChan)
	}
	return responseChan, errChan
}

// SubmitVideoSummaryJobWithCallback invokes the mts.SubmitVideoSummaryJob API asynchronously
// api document: https://help.aliyun.com/api/mts/submitvideosummaryjob.html
// asynchronous document: https://help.aliyun.com/document_detail/66220.html
func (client *Client) SubmitVideoSummaryJobWithCallback(request *SubmitVideoSummaryJobRequest, callback func(response *SubmitVideoSummaryJobResponse, err error)) <-chan int {
	result := make(chan int, 1)
	err := client.AddAsyncTask(func() {
		var response *SubmitVideoSummaryJobResponse
		var err error
		defer close(result)
		response, err = client.SubmitVideoSummaryJob(request)
		callback(response, err)
		result <- 1
	})
	if err != nil {
		defer close(result)
		callback(nil, err)
		result <- 0
	}
	return result
}

// SubmitVideoSummaryJobRequest is the request struct for api SubmitVideoSummaryJob
type SubmitVideoSummaryJobRequest struct {
	*requests.RpcRequest
	Input                string           `position:"Query" name:"Input"`
	UserData             string           `position:"Query" name:"UserData"`
	ResourceOwnerId      requests.Integer `position:"Query" name:"ResourceOwnerId"`
	ResourceOwnerAccount string           `position:"Query" name:"ResourceOwnerAccount"`
	OwnerAccount         string           `position:"Query" name:"OwnerAccount"`
	VideoSummaryConfig   string           `position:"Query" name:"VideoSummaryConfig"`
	OwnerId              requests.Integer `position:"Query" name:"OwnerId"`
	PipelineId           string           `position:"Query" name:"PipelineId"`
}

// SubmitVideoSummaryJobResponse is the response struct for api SubmitVideoSummaryJob
type SubmitVideoSummaryJobResponse struct {
	*responses.BaseResponse
	RequestId string `json:"RequestId" xml:"RequestId"`
	JobId     string `json:"JobId" xml:"JobId"`
}

// CreateSubmitVideoSummaryJobRequest creates a request to invoke SubmitVideoSummaryJob API
func CreateSubmitVideoSummaryJobRequest() (request *SubmitVideoSummaryJobRequest) {
	request = &SubmitVideoSummaryJobRequest{
		RpcRequest: &requests.RpcRequest{},
	}
	request.InitWithApiInfo("Mts", "2014-06-18", "SubmitVideoSummaryJob", "mts", "openAPI")
	return
}

// CreateSubmitVideoSummaryJobResponse creates a response to parse from SubmitVideoSummaryJob response
func CreateSubmitVideoSummaryJobResponse() (response *SubmitVideoSummaryJobResponse) {
	response = &SubmitVideoSummaryJobResponse{
		BaseResponse: &responses.BaseResponse{},
	}
	return
}