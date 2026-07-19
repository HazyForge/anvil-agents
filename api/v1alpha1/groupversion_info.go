package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion intentionally preserves the original wire identity so the
	// standalone controller can adopt existing objects without migration.
	GroupVersion  = schema.GroupVersion{Group: "control.anvil.hazyforge.io", Version: "v1alpha1"}
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(
		GroupVersion,
		&AdverseSignal{},
		&AdverseSignalList{},
		&AdverseSituation{},
		&AdverseSituationList{},
		&AgentDataVolume{},
		&AgentDataVolumeList{},
		&VolumeProfile{},
		&VolumeProfileList{},
		&AgentRunControl{},
		&AgentRunControlList{},
		&AgentSchedule{},
		&AgentScheduleList{},
		&AgentRunProfile{},
		&AgentRunProfileList{},
		&AgentHarnessProfile{},
		&AgentHarnessProfileList{},
		&AgentSkillSet{},
		&AgentSkillSetList{},
		&AgentToolSet{},
		&AgentToolSetList{},
		&AgentRun{},
		&AgentRunList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
