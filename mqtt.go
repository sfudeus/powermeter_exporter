package main

import (
	"crypto/tls"
	json "encoding/json"
	"fmt"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"
)

var mqttClient mqtt.Client
var discoveryNodeId string
var iteration int

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	log.Info("MQTT connected")
}

var connectionLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	log.Warnf("MQTT connection lost: %v", err)
}

func connectMqtt() {
	clientOptions := mqtt.NewClientOptions()
	var protocol string
	if options.MqttTls {
		protocol = "tls"
	} else {
		protocol = "tcp"
	}
	clientOptions.AddBroker(fmt.Sprintf("%s://%s:%d", protocol, options.MqttHost, options.MqttPort))
	if options.MqttTlsInsecure && options.MqttTls {
		clientOptions.SetTLSConfig(&tls.Config{InsecureSkipVerify: true})
	}
	if len(options.MqttUser) > 0 && len(options.MqttPassword) > 0 {
		clientOptions.SetUsername(options.MqttUser)
		clientOptions.SetPassword(options.MqttPassword)
	}
	clientOptions.SetOnConnectHandler(connectHandler)
	clientOptions.SetConnectionLostHandler(connectionLostHandler)
	mqttClient = mqtt.NewClient(clientOptions)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		log.Errorf("Connect to MQTT failed: %s", token.Error())
	}
	// initHomeAssistant()
}

func generateDiscoveryTopic(deviceType string, identifier string) string {
	return strings.Join([]string{options.MqttDiscoveryTopicPrefix, deviceType, discoveryNodeId, identifier, "config"}, "/")
}

func generateDevice() map[string]interface{} {
	return map[string]interface{}{
		"identifiers":  []string{discoveryNodeId},
		"manufacturer": "Stiebel Eltron",
		"name":         "ISG",
		"model":        "LWZ",
	}
}

func mapUnitToDeviceClass(unit string) interface{} {

	switch unit {
	case "°C":
		return "temperature"
	case "Hz":
		return "frequency"
	case "bar":
		return "pressure"
	case "%":
		return "humidity"
	case "kWh":
		return "energy"
	default:
		return nil
	}
}

func sendDiscoveryData(identifier string, stateTopic string, unit string) {

	if len(unit) == 0 {
		return
	}

	discoveryTopic := strings.Join([]string{options.MqttDiscoveryTopicPrefix, "sensor", discoveryNodeId, identifier, "config"}, "/")

	sensorConfigPayload := map[string]interface{}{
		"device_class":        mapUnitToDeviceClass(unit),
		"state_topic":         stateTopic,
		"unit_of_measurement": unit,
		"value_template":      "{{ value_json.Value }}",
		"name":                identifier,
		"unique_id":           discoveryNodeId + "_" + identifier,
		"object_id":           discoveryNodeId + "_" + identifier,
		"enabled_by_default":  "true",
		"device":              generateDevice(),
	}

	discoveryContent, _ := json.Marshal(sensorConfigPayload)
	mqttClient.Publish(discoveryTopic, 0, false, discoveryContent)
}

func publishData(reading meterReading) {
	topic := fmt.Sprintf("%s/%s/%s", options.MqttTopicPrefix, options.MeterName, reading.name)
	if mqttClient.IsConnected() {
		log.Debugf("Publishing %f to %s", reading.value, topic)
		t := mqttClient.Publish(topic, 0, false, fmt.Sprintf("%f", reading.value))
		go func() {
			_ = t.Wait() // Can also use '<-t.Done()' in releases > 1.2.0
			if t.Error() != nil {
				log.Error(t.Error()) // Use your preferred logging technique (or just fmt.Printf)
			}
		}()
	} else {
		log.Debugf("Not publishing to %s, not connected", topic)
	}
}
