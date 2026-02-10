package awsutil

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type ExclusionRule func(types.Instance) bool

func ExcludeByASGManaged(inst types.Instance) bool {
	for _, tag := range inst.Tags {
		if aws.ToString(tag.Key) == "aws:autoscaling:groupName" {
			return true
		}
	}
	return false
}

func ExcludeByKarpenterManaged(inst types.Instance) bool {
	for _, tag := range inst.Tags {
		if aws.ToString(tag.Key) == "karpenter.sh/nodepool" {
			return true
		}
	}
	return false
}
func ApplyExclusions(instances []types.Instance, rules ...ExclusionRule) []types.Instance {
	var filtered []types.Instance

	for _, inst := range instances {
		excluded := false

		for _, rule := range rules {
			if rule(inst) {
				excluded = true
				break
			}
		}

		if !excluded {
			filtered = append(filtered, inst)
		}
	}

	return filtered
}
