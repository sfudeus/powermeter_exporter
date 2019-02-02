#!/usr/bin/python3

from prometheus_client import Gauge, MetricsHandler, core
from http.server import BaseHTTPRequestHandler, HTTPServer
from socketserver import ThreadingMixIn

import argparse
import serial
import sys
import threading
import time
import logging

class PowerMetricsProcessor(threading.Thread):

  meter_name = ''
  device = ''
  gauge_power = Gauge('powermeter_power', 'Power reading of meter', ['meter_name'])
  gauge_work = Gauge('powermeter_work', 'Work reading of meter', ['meter_id', 'meter_name'])

  def run(self):
    logging.info("Starting thread for data gathering")
    while True:
      self.generateMetrics()
      time.sleep(60)

  def __init__(self, meter_name, device):
    super().__init__()
    self.meter_name = meter_name
    self.device = device

  def generateMetrics(self):
    logging.info("Gathering metrics")

    with serial.Serial(self.device, 9600, xonxoff=0, rtscts=0, bytesize=8, parity='N', stopbits=1) as ser:
      s = ser.read_until(bytes.fromhex('01010101 0a'), 1000)
      work0_index = s.find(bytes.fromhex('77070100010800ff0101621e52ff56'))
      work1_index = s.find(bytes.fromhex('77070100010801ff0101621e52ff56'))
      work2_index = s.find(bytes.fromhex('77070100010802ff0101621e52ff56'))
      power_index = s.find(bytes.fromhex('77070100100700ff'))
      phase1_index = s.find(bytes.fromhex('77070100240700ff'))

      logging.debug("Work0 is at %d, Work1 is at %d, Work2 is at %d, Power is at %d, Phase1 is at %d" % (work0_index, work1_index, work2_index, power_index, phase1_index))
      logging.debug("P1: {}".format(s[work1_index:work1_index+25]))
      logging.debug("P2: {}".format(s[work2_index:work2_index+25]))
      logging.debug("W: {}".format(s[power_index:power_index+25]))
      logging.debug("P1: {}".format(s[work1_index+15:work1_index+20]))
      logging.debug("P2: {}".format(s[work2_index+15:work2_index+20]))
      logging.debug("W: {}".format(s[power_index+15:power_index+20]))

      if work0_index >=0:
        work0 = s[work0_index+15:work0_index+20]
        self.gauge_work.labels(meter_name=self.meter_name, meter_id="1.8.0").set(int.from_bytes(work0, byteorder='big')/10000)
      if work1_index >=0:
        work1 = s[work1_index+15:work1_index+20]
        self.gauge_work.labels(meter_name=self.meter_name, meter_id="1.8.1").set(int.from_bytes(work1, byteorder='big')/10000)
      if work2_index >=0:
        work2 = s[work2_index+15:work2_index+20]
        self.gauge_work.labels(meter_name=self.meter_name, meter_id="1.8.2").set(int.from_bytes(work2, byteorder='big')/10000)
      if power_index >=0:
        power = s[power_index+15:power_index+19]
        self.gauge_power.labels(meter_name=self.meter_name).set(int.from_bytes(power, byteorder='big')/10)

    logging.info("Done gathering metrics")

class _ThreadingSimpleServer(ThreadingMixIn, HTTPServer):
  """Thread per request HTTP server."""

def start_http_server(port, addr='', registry=core.REGISTRY):
  """Starts an HTTP server for prometheus metrics as a daemon thread"""
  httpd = _ThreadingSimpleServer((addr, port), MetricsHandler.factory(registry))
  t = threading.Thread(target=httpd.serve_forever)
  t.daemon = False
  t.start()

def start_gathering(meter_name, device):
  t = PowerMetricsProcessor(meter_name, device)
  t.start()

if __name__ == '__main__':

  parser = argparse.ArgumentParser()
  parser.add_argument("--meter_name", help="The name of the meter which is monitored")
  parser.add_argument("--device", help="The name of the device which is monitored", default='/dev/ttyUSB0')
  parser.add_argument("--port", help="The port where to expose the exporter", default=8010)
  parser.add_argument("--debug", action="store_true")
  args = parser.parse_args()

  if args.debug:
    logging.basicConfig(level=logging.DEBUG)

  start_gathering(args.meter_name, args.device)

  # Start up the server to expose the metrics.
  start_http_server(int(args.port))

