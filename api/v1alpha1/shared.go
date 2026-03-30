package v1alpha1

func KindOf(obj interface{}) string {
	var kind string
	switch obj.(type) {
	case *HibernatePlan:
		kind = "HibernatePlan"
	case *ScheduleException:
		kind = "ScheduleException"
	case *CloudProvider:
		kind = "CloudProvider"
	case *K8SCluster:
		kind = "K8SCluster"
	case *HibernateNotification:
		kind = "HibernateNotification"
	default:
		kind = "Unknown"
	}

	return kind
}
