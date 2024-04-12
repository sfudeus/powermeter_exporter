package main

import (
	"crypto/tls"
	json "encoding/json"
	"fmt"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	log "github.com/sirupsen/logrus"
)

var mqttClient mqtt.Client

var (
	gaugeMqttConnected = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "powermeter",
		Name:      "mqtt_connected",
		Help:      "Status of the MQTT connection",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
		})
	counterMqttMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "powermeter",
		Name:      "mqtt_messages",
		Help:      "Number of MQTT message sent",
	},
		[]string{
			//manual name of the meter, to distinguish between multiple sensors
			"meter_name",
		})
)

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
	log.Info("MQTT connected")
}

var connectionLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
	log.Warnf("MQTT connection lost: %v", err)
	gaugeMqttConnected.WithLabelValues(options.MeterName).Set(0)
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
	} else {
		gaugeMqttConnected.WithLabelValues(options.MeterName).Set(1)
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

	if mqttClient == nil || !mqttClient.IsConnected() {
		log.Info("MQTTClient not initialized or disconnected, reconnecting")
		connectMqtt()
	}

	topic := fmt.Sprintf("%s/%s/%s", options.MqttTopicPrefix, options.MeterName, reading.name)
	if mqttClient != nil && mqttClient.IsConnected() {
		log.Debugf("Publishing %f to %s", reading.value, topic)
		t := mqttClient.Publish(topic, 0, false, fmt.Sprintf("%f", reading.value))
		counterMqttMessages.WithLabelValues(options.MeterName).Inc()
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
