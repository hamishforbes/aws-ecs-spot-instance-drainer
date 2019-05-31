package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	// "strings"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
)

type instance struct {
	Cluster string `json:"Cluster"`
	Arn     string `json:"ContainerInstanceArn"`
}

func getContainerInstance() instance {
	client := http.Client{
		Timeout: time.Second * 2, // Maximum of 2 secs
	}
	containerInstance := instance{}

	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:51678/v1/metadata", nil)
	if err != nil {
		log.Fatal(err)
	}

	res, getErr := client.Do(req)
	if getErr != nil {
		fmt.Println(getErr)
		return containerInstance
		// log.Fatal(getErr)
	}

	body, readErr := ioutil.ReadAll(res.Body)
	if readErr != nil {
		log.Fatal(readErr)
	}

	jsonErr := json.Unmarshal(body, &containerInstance)
	if jsonErr != nil {
		log.Fatal(jsonErr)
	}

	fmt.Printf("HTTP: %s\n", res.Status)
	return containerInstance
}

func isStopping() bool {
	client := http.Client{
		Timeout: time.Second * 2, // Maximum of 2 secs
	}

	ec2URL := os.Getenv("EC2METADATA_URL")
	if ec2URL == "" {
		ec2URL = "169.254.169.254"
	}

	url := fmt.Sprintf("http://%s/latest/meta-data/spot/instance-action", ec2URL)
	req, err := http.NewRequest(http.MethodGet, url, nil)

	if err != nil {
		log.Fatal(err)
	}

	res, getErr := client.Do(req)
	if getErr != nil {
		log.Fatal(getErr)
	}

	return res.StatusCode == 200
}

func webhook(url string) bool {
	fmt.Printf("Triggering webhook: %s\n", url)

	client := http.Client{
		Timeout: time.Second * 60, // Maximum of 2 secs
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer([]byte(os.Getenv("WEBHOOK_DATA"))))

	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	res, getErr := client.Do(req)
	if getErr != nil {
		log.Fatal(getErr)
	}

	fmt.Printf("Webhook response: %s\n", res.Status)
	return res.StatusCode == 200
}

func drain(containerInstance instance) {
	fmt.Printf("Draining %s\n", containerInstance.Arn)
	// drain this one
	// ecs stuff
	svc := ecs.New(session.New())

	input := &ecs.UpdateContainerInstancesStateInput{
		ContainerInstances: []*string{aws.String(containerInstance.Arn)},
		Cluster:            aws.String(containerInstance.Cluster),
		Status:             aws.String("DRAINING"),
	}

	req, resp := svc.UpdateContainerInstancesStateRequest(input)

	err := req.Send()
	if err != nil { // resp is now filled
		fmt.Println(resp)
		fmt.Println(err)
	}

	fmt.Println("Successfully drained the instance")

}

func writePrometheusMetric(metric string, val uint8) {
	fmt.Printf("Writing Prometheus Metric: %s %d\n", metric, val)

	message := []byte(fmt.Sprintf("%s %d\n", metric, val))

	promDir := os.Getenv("PROMETHEUS_TEXTFILE_DIR")
	if promDir == "" {
		promDir = "/var/lib/node_exporter"
	}

	path := fmt.Sprintf("%s/%s.prom", promDir, metric)
	err := ioutil.WriteFile(path, message, 0644)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	webhookURL := os.Getenv("WEBHOOK_URL")
	mockTerminate := (os.Getenv("MOCK_TERMINATE") != "")
	disableDrain := (os.Getenv("DISABLE_DRAIN") != "")
	prometheusEnabled := (os.Getenv("USE_PROMETHEUS") != "")

	fmt.Printf("Prometheus:  %t\n", prometheusEnabled)
	fmt.Printf("Mock Terminate:  %t\n", mockTerminate)
	fmt.Printf("Disable Draining:  %t\n", disableDrain)
	fmt.Printf("Webhook URL:  %s\n", webhookURL)

	containerInstance := instance{}

	if !disableDrain {
		containerInstance := getContainerInstance()

		for containerInstance == (instance{}) {
			fmt.Println("Cannot communicate with ECS Agent. Retrying...")
			time.Sleep(time.Second * 5)
			containerInstance = getContainerInstance()
		}

		fmt.Printf("Found ECS Container Instance %s\n", containerInstance.Arn)
		fmt.Printf("on the %s cluster.\n", containerInstance.Cluster)
	}

	if prometheusEnabled {
		writePrometheusMetric("ecs_spot_instance_terminating", 0)
	}

	for true {
		if mockTerminate || isStopping() {
			fmt.Println("Spot instance is terminating.")

			if prometheusEnabled {
				writePrometheusMetric("ecs_spot_instance_terminating", 1)
			}

			if webhookURL != "" {
				webhook(webhookURL)
			}

			if !disableDrain {
				drain(containerInstance)
			}

			os.Exit(0)
		}

		time.Sleep(time.Second * 5)
	}
}
