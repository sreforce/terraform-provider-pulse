terraform {
  required_providers {
    pulse = {
      source = "sreforce/pulse"
    }
  }
}

# PULSE_API_URL and PULSE_API_TOKEN are read from the environment.
provider "pulse" {}

