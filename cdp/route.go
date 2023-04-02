package cdp

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"gitee.com/baixudong/gospider/re"
	"gitee.com/baixudong/gospider/requests"
	"gitee.com/baixudong/gospider/tools"
	"golang.org/x/exp/maps"
)

type RequestOption struct {
	Url      string            `json:"url"`
	Method   string            `json:"method"`
	PostData string            `json:"postData"`
	Headers  map[string]string `json:"headers"`
}
type RequestData struct {
	Url              string            `json:"url"`
	UrlFragment      string            `json:"urlFragment"`
	Method           string            `json:"method"`
	Headers          map[string]string `json:"headers"`
	PostData         string            `json:"postData"`
	HasPostData      bool              `json:"hasPostData"`
	MixedContentType string            `json:"mixedContentType"`
	InitialPriority  string            `json:"initialPriority"` //初始优先级
	ReferrerPolicy   string            `json:"referrerPolicy"`
	IsLinkPreload    bool              `json:"isLinkPreload"`   //是否通过链路预加载加载。
	PostDataEntries  []DataEntrie      `json:"postDataEntries"` //是否通过链路预加载加载。
}
type RouteData struct {
	RequestId    string      `json:"requestId"`
	Request      RequestData `json:"request"`
	FrameId      string      `json:"frameId"`
	ResourceType string      `json:"resourceType"`
	NetworkId    string      `json:"networkId"`

	ResponseErrorReason string   `json:"responseErrorReason"`
	ResponseStatusCode  int      `json:"responseStatusCode"`
	ResponseStatusText  string   `json:"responseStatusText"`
	ResponseHeaders     []Header `json:"responseHeaders"`
}

type Route struct {
	webSock  *WebSock
	recvData RouteData
}

func (obj *Route) NewRequestOption() RequestOption {
	return RequestOption{
		Url:      obj.Url(),
		Method:   obj.Method(),
		PostData: obj.PostData(),
		Headers:  obj.Headers(),
	}
}

func (obj *Route) ResourceType() string {
	return obj.recvData.ResourceType
}
func (obj *Route) Url() string {
	return obj.recvData.Request.Url
}
func (obj *Route) Method() string {
	return obj.recvData.Request.Method
}
func (obj *Route) PostData() string {
	return obj.recvData.Request.PostData
}
func (obj *Route) Headers() map[string]string {
	if _, ok := obj.recvData.Request.Headers["If-Modified-Since"]; ok {
		delete(obj.recvData.Request.Headers, "If-Modified-Since")
	}
	if _, ok := obj.recvData.Request.Headers["Accept-Language"]; !ok {
		obj.recvData.Request.Headers["Accept-Language"] = requests.AcceptLanguage
	}
	return obj.recvData.Request.Headers
}
func (obj *Route) Cookies() requests.Cookies {
	if cook, ok := obj.recvData.Request.Headers["Cookie"]; ok {
		return requests.ReadCookies(cook)
	}
	return nil
}
func keyMd5(key RequestOption, resourceType string) [16]byte {
	var md5Str string
	nt := strconv.Itoa(int(time.Now().Unix() / 1000))
	key.Url = re.Sub(fmt.Sprintf(`=%s\d*?&`, nt), "=&", key.Url)
	key.Url = re.Sub(fmt.Sprintf(`=%s\d*?$`, nt), "=", key.Url)

	key.Url = re.Sub(fmt.Sprintf(`=%s\d*?\.\d+?&`, nt), "=&", key.Url)
	key.Url = re.Sub(fmt.Sprintf(`=%s\d*?\.\d+?$`, nt), "=", key.Url)

	key.Url = re.Sub(`=0\.\d{10,}&`, "=&", key.Url)
	key.Url = re.Sub(`=0\.\d{10,}$`, "=", key.Url)

	md5Str += fmt.Sprintf("%s,%s,%s", key.Method, key.Url, key.PostData)

	if resourceType == "Document" || resourceType == "XHR" {
		kks := maps.Keys(key.Headers)
		sort.Strings(kks)
		for _, k := range kks {
			md5Str += fmt.Sprintf("%s,%s", k, key.Headers[k])
		}
	}
	return tools.Md5(md5Str)
}
func (obj *Route) Request(ctx context.Context, routeOption RequestOption, options ...requests.RequestOption) (FulData, error) {
	option := requests.RequestOption{
		Proxy:   obj.webSock.option.Proxy,
		Ja3Spec: obj.webSock.option.Ja3Spec,
		Ja3:     obj.webSock.option.Ja3,
	}
	if len(options) > 0 {
		option = options[0]
	}
	if routeOption.PostData != "" {
		option.Bytes = tools.StringToBytes(routeOption.PostData)
	}
	option.Headers = routeOption.Headers
	resourceType := obj.ResourceType()
	if resourceType == "Document" || resourceType == "XHR" {
		option.TryNum = 2
	} else {
		option.TryNum = 1
	}
	var fulData FulData
	var err error
	routeKey := keyMd5(routeOption, resourceType)
	if !obj.webSock.option.DisDataCache {
		if fulData, err = obj.webSock.db.Get(routeKey); err == nil { //如果有緩存
			return fulData, err
		}
	}
	rs, err := obj.webSock.reqCli.Request(ctx, routeOption.Method, routeOption.Url, option)
	if err != nil {
		return fulData, err
	}
	headers := []Header{}
	for kk, vvs := range rs.Headers() {
		for _, vv := range vvs {
			headers = append(headers, Header{
				Name:  kk,
				Value: vv,
			})
		}
	}
	fulData.StatusCode = rs.StatusCode()
	fulData.Body = rs.Text()
	fulData.Headers = headers
	fulData.ResponsePhrase = rs.Status()
	if !obj.webSock.option.DisDataCache && fulData.StatusCode == 200 && fulData.Body != "" {
		obj.webSock.db.Put(routeKey, fulData)
	}
	return fulData, nil
}

func (obj *Route) FulFill(ctx context.Context, fulDatas ...FulData) error {
	var fulData FulData
	if len(fulDatas) > 0 {
		fulData = fulDatas[0]
	}
	if _, err := obj.webSock.FetchFulfillRequest(ctx, obj.recvData.RequestId, fulData); err != nil {
		if err2 := obj.Fail(nil); err2 != nil {
			return err2
		}
		return err
	}
	return nil
}
func (obj *Route) Continue(ctx context.Context) error {
	fulData, err := obj.Request(ctx, obj.NewRequestOption())
	var err2 error
	if err != nil {
		err2 = obj.Fail(ctx)
	} else {
		err = obj.FulFill(ctx, fulData)
	}
	if err != nil {
		return err
	}
	return err2
}

func (obj *Route) _continue(ctx context.Context) error {
	_, err := obj.webSock.FetchContinueRequest(ctx, obj.recvData.RequestId)
	return err
}

// Failed, Aborted, TimedOut, AccessDenied, ConnectionClosed, ConnectionReset, ConnectionRefused, ConnectionAborted, ConnectionFailed, NameNotResolved, InternetDisconnected, AddressUnreachable, BlockedByClient, BlockedByResponse
func (obj *Route) Fail(ctx context.Context, errorReasons ...string) error {
	var errorReason string
	if len(errorReasons) > 0 {
		errorReason = errorReasons[0]
	}
	if errorReason == "" {
		errorReason = "Failed"
	}
	_, err := obj.webSock.FetchFailRequest(ctx, obj.recvData.RequestId, errorReason)
	return err
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type Headers []Header

// 获取符合key 条件的所有headers
func (obj Headers) Gets(name string) Headers {
	var result Headers
	for _, head := range obj {
		if head.Name == name {
			result = append(result, head)
		}
	}
	return result
}

// 获取符合key 条件的cookies
func (obj Headers) Get(name string) (Header, bool) {
	vals := obj.Gets(name)
	if i := len(vals); i == 0 {
		return Header{}, false
	} else {
		return vals[i-1], true
	}
}

// 获取符合key 条件的所有cookies的值
func (obj Headers) GetVals(name string) []string {
	var result []string
	for _, cook := range obj {
		if cook.Name == name {
			result = append(result, cook.Value)
		}
	}
	return result
}

// 获取符合key 条件的cookies的值
func (obj Headers) GetVal(name string) (string, bool) {
	vals := obj.GetVals(name)
	if i := len(vals); i == 0 {
		return "", false
	} else {
		return vals[i-1], true
	}
}

type FulData struct {
	StatusCode     int     `json:"responseCode"`
	Headers        Headers `json:"responseHeaders"`
	Body           string  `json:"body"`
	ResponsePhrase string  `json:"responsePhrase"`
}
