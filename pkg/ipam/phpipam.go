package ipam

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("phpipam")

type PhpIPAM struct {
	PhpIPAMConfig *PhpIPAMConfigSpec `yaml:"phpIPAM,omitempty"`
}

type PhpIPAMConfigSpec struct {
	username *string `yaml:"username"`
	password *string `yaml:"password"`

	URL   *string `yaml:"url"`
	AppID *string `yaml:"appID"`

	// map of zone to subnet IDs, e.g. "wdc04": ["7", "8", "9"]
	SubnetMap map[string][]int `yaml:"subnetMap"`

	token string
}

type phpIPAMResponse struct {
	Code int `json:"code"`
	Success interface{} `json:"success"`
	Data interface{} `json:"data,omitempty"`
	Message string `json:"message,omitempty"`
	Time float64 `json:"time"`
}

type PhpIPAMSubnet struct {
	ID string `json:"id,omitempty"`
	Subnet string `json:"subnet,omitempty"`
	Mask string `json:"mask,omitempty"`
}

type PhpIPAMAddress struct {
	ID string `json:"id,omitempty"`
	IPAddr string `json:"ip_addr,omitempty"`
	SubnetId string `json:"subnetId,omitempty"`
}

var config PhpIPAM

func NewPhpIPAM() (*PhpIPAM, error) {
	config := &PhpIPAM{}

	yamlFile, err := ioutil.ReadFile("/opt/controller-config/overlay-ip-config.yaml")
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(yamlFile, config)
	if err != nil {
		return nil, err
	}

	// read in the username/password from environment
	if config.PhpIPAMConfig.username == nil {
		envUsername := os.Getenv("PHPIPAM_USERNAME")
		config.PhpIPAMConfig.username = &envUsername
	}

	if config.PhpIPAMConfig.password == nil {
		envPassword := os.Getenv("PHPIPAM_PASSWORD")
		config.PhpIPAMConfig.password = &envPassword
	}

	if len(config.PhpIPAMConfig.SubnetMap) == 0 {
		return nil, fmt.Errorf("Subnet Map is empty; expected map of zones to subnet IDs")
	}

	// attempt to populate the token
	err = config.getToken()
	if err != nil {
		return nil, err
	}

	return config, nil
}

func (p *PhpIPAM) callAPI(httpVerb string, path string, params map[string]string) (*phpIPAMResponse, error) {
	var httpClient = &http.Client{
		Timeout: time.Second * 10,
	}

	var sb strings.Builder
	for key, val := range params {
		sb.WriteString(fmt.Sprintf("%s=%s\n", key, val))
	}

	paramString := sb.String()
	fullPath := fmt.Sprintf("%s/%s", *p.PhpIPAMConfig.URL, path)
	request, err := http.NewRequest(
		httpVerb,
		fullPath,
		bytes.NewBufferString(paramString))
	if err != nil {
		return nil, err
	}

	request.Header.Add("token", p.PhpIPAMConfig.token)

	log.Info(fmt.Sprintf("Calling phpIPAM: %s", fullPath))
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	resp := &phpIPAMResponse{}
	err = json.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (p *PhpIPAM) getToken() error {
	var httpClient = &http.Client{
		Timeout: time.Second * 10,
	}

	request, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/%s/user/", *p.PhpIPAMConfig.URL, *p.PhpIPAMConfig.AppID), nil)
	request.SetBasicAuth(*p.PhpIPAMConfig.username, *p.PhpIPAMConfig.password)

	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	resp := &phpIPAMResponse{}
	err = json.Unmarshal(body, resp)

	if !resp.isSuccess() {
		return fmt.Errorf("Error retrieving token, response was %b", resp)
	}

	token, err := resp.getValue("token")
	if err != nil {
		return err
	}

	p.PhpIPAMConfig.token = token.(string)

	return nil
}

func (r *phpIPAMResponse) isSuccess() bool {
	// for some reason, phpipam may return bools or ints in "success" responses.  try
	// to decode them here
	success := r.Success
	switch success.(type) {
	case bool:
		return success.(bool)
	case int:
		return success == 0
	}

	return false
}

func (r *phpIPAMResponse) getValue(key string) (interface{}, error) {
	// split the key on the "." character
	splits := strings.Split(key, ".")

	tmpVal := r.Data.(map[string]interface{})
	for _, s := range splits {
		tmp := tmpVal[s]
		if tmp == nil {
			return nil, fmt.Errorf("Cannot find key %s in response %b", key, r.Data)
		}

		switch tmp.(type) {
		case map[string]interface{}:
			// if it's another map, keep going down the path
			tmpVal = tmp.(map[string]interface{})
		default:
			return tmp, nil
		}
	}

	return tmpVal, nil
}

func (p *PhpIPAM) GetSubnetForIP(ipAddr string) (map[string]string, error) {
	returnMap := make(map[string]string)

	// find the subnet ids
	resp, err := p.callAPI(http.MethodGet,
		fmt.Sprintf("/api/%s/addresses/search/%s/", *p.PhpIPAMConfig.AppID, ipAddr),
		map[string]string{},
	)

	if err != nil {
		return returnMap, err
	}

	if !resp.isSuccess() {
		return returnMap, fmt.Errorf("Unable to find subnet for IP %s: %s", ipAddr, resp.Message)
	}

	// get the first subnets that have this IP address
	ipaddrs := resp.Data.([]interface{})
	for _, ipaddr := range ipaddrs {
		ipmap := ipaddr.(map[string]interface{})
		subnetid := ipmap["subnetId"].(string)

		// get the subnet gateway
		subnetresp, err := p.callAPI(http.MethodGet,
			fmt.Sprintf("/api/%s/subnets/%s/", *p.PhpIPAMConfig.AppID, subnetid),
			map[string]string{},
		)

		if !subnetresp.isSuccess() {
			log.Info(fmt.Sprintf("unable to get subnet %d for ip %s: %s", subnetid, ipaddr, subnetresp.Message))
			continue
		}

		mask, err := subnetresp.getValue("mask")
		if err != nil {
			return returnMap, err
		}

		subnet, err := subnetresp.getValue("subnet")
		if err != nil {
			return returnMap, err
		}

		gateway, err := subnetresp.getValue("gateway.ip_addr")
		if err != nil {
			return returnMap, err
		}

		returnMap["subnet"] = subnet.(string)
		returnMap["mask"] = mask.(string)
		returnMap["gateway"] = gateway.(string)

		return returnMap, nil
	}

	return returnMap, fmt.Errorf("unable to find subnets for IP all subnets return error")
}

func (p *PhpIPAM) ReserveIPAddress(owner string, zone string) (string, error) {
	// find the subnet ids
	log.Info("Reserve IP in zone", "zone", zone, "owner", owner)

	subnetIds := p.PhpIPAMConfig.SubnetMap[zone]

	for _, subnetId := range subnetIds {
		log.Info("Trying to reserve IP in subnet", "subnet", subnetId, "zone", zone, "owner", owner)
		resp, err := p.callAPI(http.MethodPost,
			fmt.Sprintf("/api/%s/addresses/first_free/%d/", *p.PhpIPAMConfig.AppID, subnetId),
			map[string]string{
				"owner": owner,
			},
		)

		if err != nil {
			return "", err
		}

		if !resp.isSuccess() {
			log.Info(fmt.Sprintf("Unable to reserve IP on subnet %d", subnetId), "zone", zone, "message", resp.Message)
			continue
		}

		// get the IP
		ipAddr := resp.Data.(string)
		// get the subnet mask
		subnetResp, err := p.callAPI(http.MethodGet,
			fmt.Sprintf("/api/%s/subnets/%d/", *p.PhpIPAMConfig.AppID, subnetId),
			map[string]string{},
		)

		if !subnetResp.isSuccess() {
			log.V(1).Info(fmt.Sprintf("Unable to get subnet %d for IP %s: %s", subnetId, ipAddr, subnetResp.Message))
			continue
		}

		mask, err := subnetResp.getValue("mask")
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("%s/%s", ipAddr, mask.(string)), nil
	}

	return "", fmt.Errorf("unable to reserve IP in zone %s, all subnets return error", zone)
}

func (p *PhpIPAM) DeleteIPAddress(ipAddr string) (error) {
	// find the subnet 
	resp, err := p.callAPI(http.MethodPost,
		fmt.Sprintf("/api/%s/addresses/search/%s/", *p.PhpIPAMConfig.AppID, ipAddr),
		map[string]string{},
	)

	if err != nil {
		return err
	}

	if !resp.isSuccess() {
		if strings.Contains(resp.Message, "Address not found") {
			log.Info(fmt.Sprintf("Unable to find IP %s ", ipAddr), "message", resp.Message)
			return nil
		} else {
			return err
		}
	}

	// get the first subnets that have this IP address
	ipaddrs := resp.Data.([]interface{})
	for _, ipaddr := range ipaddrs {
		ipmap := ipaddr.(map[string]interface{})
		id := ipmap["id"].(string)

		// get the subnet gateway
		subnetresp, err := p.callAPI(http.MethodDelete,
			fmt.Sprintf("/api/%s/addresses/%s/", *p.PhpIPAMConfig.AppID, id),
			map[string]string{},
		)

		if err != nil {
			return err
		}

		if !subnetresp.isSuccess() {
			log.Info(fmt.Sprintf("unable to get delete ip %s: %s", ipaddr, subnetresp.Message))
			continue
		}

		return nil
	}

	return fmt.Errorf("unable to delete IP")
}
