/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package awsutil

// AWSConnectorConfig holds AWS connector settings.
type AWSConnectorConfig struct {
	Region          string
	AccountID       string
	AssumeRoleArn   string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}
