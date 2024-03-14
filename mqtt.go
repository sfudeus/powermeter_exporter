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
}

func generateDevice() map[string]interface{} {
	return map[string]interface{}{
		"identifiers": []string{options.MeterName},
		"name":        "Powermeter",
	}
}

func sendDiscoveryData(identifier string, stateTopic string) {

	oid := strings.Replace(identifier, ".", "_", -1)

	discoveryTopic := strings.Join([]string{options.MqttDiscoveryTopicPrefix, "sensor", options.MeterName, oid, "config"}, "/")

	sensorConfigPayload := map[string]interface{}{
		"device_class":        "energy",
		"state_class":         "total_increasing",
		"state_topic":         stateTopic,
		"unit_of_measurement": "kWh",
		"name":                identifier,
		"unique_id":           options.MeterName + "_" + oid,
		"object_id":           options.MeterName + "_" + oid,
		"enabled_by_default":  "true",
		"device":              generateDevice(),
	}

	discoveryContent, _ := json.Marshal(sensorConfigPayload)
	mqttClient.Publish(discoveryTopic, 0, false, discoveryContent)
}

func publishData(reading meterReading, iteration int) {
	withDiscoveryData := (iteration%10 == 0)
	log.Debugf("publishing in iteration %d, with discovery set to %t", iteration, withDiscoveryData)

	topic := fmt.Sprintf("%s/%s/%s", options.MqttTopicPrefix, options.MeterName, reading.name)
	if mqttClient != nil && mqttClient.IsConnected() {
		log.Debugf("Publishing %f to %s", reading.value, topic)
		t := mqttClient.Publish(topic, 0, false, fmt.Sprintf("%f", reading.value))
		go func() {
			_ = t.Wait() // Can also use '<-t.Done()' in releases > 1.2.0
			if t.Error() != nil {
				log.Error(t.Error()) // Use your preferred logging technique (or just fmt.Printf)
			}
		}()

		if withDiscoveryData {
			sendDiscoveryData(reading.name, topic)
		}

	} else {
		log.Debugf("Not publishing to %s, not connected", topic)
	}
}
