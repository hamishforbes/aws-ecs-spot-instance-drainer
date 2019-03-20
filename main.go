package main

import (
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

	fmt.Println("Checking spot status...")
	return res.StatusCode == 200
}

func drain(containerInstance instance) {
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
	enabled := os.Getenv("USE_PROMETHEUS")
	if enabled == "" {
		return
	}

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
	writePrometheusMetric("ecs_spot_instance_terminating", 0)

	containerInstance := getContainerInstance()

	for containerInstance == (instance{}) {
		fmt.Println("Cannot communicate with ECS Agent. Retrying...")
		time.Sleep(time.Second * 5)
		containerInstance = getContainerInstance()
	}

	fmt.Printf("Found ECS Container Instance %s\n", containerInstance.Arn)
	fmt.Printf("on the %s cluster.\n", containerInstance.Cluster)

	for true {
		if isStopping() {

			writePrometheusMetric("ecs_spot_instance_terminating", 1)

			fmt.Println("Spot instance is being acted upon. Doing something")
			fmt.Printf("Drain this %s\n", containerInstance.Arn)
			// drain this one
			drain(containerInstance)

			os.Exit(0)
		}

		time.Sleep(time.Second * 5)
	}
}
