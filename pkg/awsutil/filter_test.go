package awsutil

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
)

func TestExcludeByASGManaged(t *testing.T) {
	tests := []struct {
		name     string
		instance types.Instance
		want     bool
	}{
		{
			name: "managed by ASG",
			instance: types.Instance{
				Tags: []types.Tag{
					{Key: aws.String("aws:autoscaling:groupName"), Value: aws.String("my-asg")},
				},
			},
			want: true,
		},
		{
			name: "not managed by ASG",
			instance: types.Instance{
				Tags: []types.Tag{
					{Key: aws.String("Name"), Value: aws.String("my-instance")},
				},
			},
			want: false,
		},
		{
			name:     "no tags",
			instance: types.Instance{},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExcludeByASGManaged(tt.instance)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExcludeByKarpenterManaged(t *testing.T) {
	tests := []struct {
		name     string
		instance types.Instance
		want     bool
	}{
		{
			name: "managed by Karpenter",
			instance: types.Instance{
				Tags: []types.Tag{
					{Key: aws.String("karpenter.sh/nodepool"), Value: aws.String("default")},
				},
			},
			want: true,
		},
		{
			name: "not managed by Karpenter",
			instance: types.Instance{
				Tags: []types.Tag{
					{Key: aws.String("Name"), Value: aws.String("my-instance")},
				},
			},
			want: false,
		},
		{
			name:     "no tags",
			instance: types.Instance{},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExcludeByKarpenterManaged(tt.instance)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyExclusions(t *testing.T) {
	inst1 := types.Instance{
		InstanceId: aws.String("i-1"),
		Tags: []types.Tag{
			{Key: aws.String("Name"), Value: aws.String("normal")},
		},
	}
	instASG := types.Instance{
		InstanceId: aws.String("i-asg"),
		Tags: []types.Tag{
			{Key: aws.String("aws:autoscaling:groupName"), Value: aws.String("my-asg")},
		},
	}
	instKarpenter := types.Instance{
		InstanceId: aws.String("i-karpenter"),
		Tags: []types.Tag{
			{Key: aws.String("karpenter.sh/nodepool"), Value: aws.String("default")},
		},
	}

	instances := []types.Instance{inst1, instASG, instKarpenter}

	tests := []struct {
		name  string
		rules []ExclusionRule
		want  []string
	}{
		{
			name:  "no rules",
			rules: nil,
			want:  []string{"i-1", "i-asg", "i-karpenter"},
		},
		{
			name:  "exclude ASG",
			rules: []ExclusionRule{ExcludeByASGManaged},
			want:  []string{"i-1", "i-karpenter"},
		},
		{
			name:  "exclude Karpenter",
			rules: []ExclusionRule{ExcludeByKarpenterManaged},
			want:  []string{"i-1", "i-asg"},
		},
		{
			name:  "exclude both",
			rules: []ExclusionRule{ExcludeByASGManaged, ExcludeByKarpenterManaged},
			want:  []string{"i-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyExclusions(instances, tt.rules...)
			var gotIDs []string
			for _, inst := range got {
				gotIDs = append(gotIDs, *inst.InstanceId)
			}
			assert.Equal(t, tt.want, gotIDs)
		})
	}
}
