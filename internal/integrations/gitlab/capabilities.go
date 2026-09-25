package gitlab

import "sort"

// Feature — то, что умеет экран: список MR, обсуждение, merge и так далее.
type Feature string

const (
	FeatureWhoAmI        Feature = "whoami"
	FeatureProject       Feature = "project"
	FeatureMergeRequests Feature = "mergeRequests"
	FeatureMergeRequest  Feature = "mergeRequest"
	FeatureApprovals     Feature = "approvals"
	FeatureDiscussions   Feature = "discussions"
	FeatureChanges       Feature = "changes"
	FeatureDiff          Feature = "diff"
	FeatureFile          Feature = "file"
	FeatureComment       Feature = "comment"
	FeatureReply         Feature = "reply"
	FeatureApprove       Feature = "approve"
	FeatureMerge         Feature = "merge"
	FeaturePipelines     Feature = "pipelines"
	FeatureMRPipelines   Feature = "mrPipelines"
	FeatureJobs          Feature = "jobs"
	FeatureJobLog        Feature = "jobLog"
	FeatureRetry         Feature = "retry"
)

// featureTools — инструменты каждой возможности. Внутренний список —
// альтернативы: хватает первого найденного (diff отдают два инструмента).
// Внешний — всё сразу: одобрение требует и «одобрить», и «снять».
var featureTools = map[Feature][][]string{
	FeatureWhoAmI:        {{"whoami"}},
	FeatureProject:       {{"get_project"}},
	FeatureMergeRequests: {{"list_merge_requests"}},
	FeatureMergeRequest:  {{"get_merge_request"}},
	FeatureApprovals:     {{"get_merge_request_approval_state"}},
	FeatureDiscussions:   {{"mr_discussions"}},
	FeatureChanges:       {{"list_merge_request_changed_files"}},
	FeatureDiff:          {{"get_merge_request_file_diff", "get_merge_request_diffs"}},
	FeatureFile:          {{"get_file_contents"}},
	FeatureComment:       {{"create_merge_request_note"}},
	FeatureReply:         {{"create_merge_request_discussion_note"}},
	FeatureApprove:       {{"approve_merge_request"}, {"unapprove_merge_request"}},
	FeatureMerge:         {{"merge_merge_request"}},
	FeaturePipelines:     {{"list_pipelines"}, {"get_pipeline"}},
	FeatureMRPipelines:   {{"list_merge_request_pipelines"}},
	FeatureJobs:          {{"list_pipeline_jobs"}},
	FeatureJobLog:        {{"get_pipeline_job_output"}},
	FeatureRetry:         {{"retry_pipeline_job"}},
}

// Capability — есть ли возможность у подключённого сервера и чем она сделана.
type Capability struct {
	Available bool     `json:"available"`
	Tools     []string `json:"tools,omitempty"`
	Missing   []string `json:"missing,omitempty"`
}

// Capabilities сверяет список инструментов сервера с возможностями экранов.
// Недостающий инструмент выключает одну возможность, а не весь плагин.
func Capabilities(available []string) map[Feature]Capability {
	have := map[string]bool{}
	for _, name := range available {
		have[name] = true
	}
	result := map[Feature]Capability{}
	for feature, groups := range featureTools {
		capability := Capability{Available: true}
		for _, alternatives := range groups {
			found := ""
			for _, tool := range alternatives {
				if have[tool] {
					found = tool
					break
				}
			}
			if found == "" {
				capability.Available = false
				capability.Missing = append(capability.Missing, alternatives[0])
				continue
			}
			capability.Tools = append(capability.Tools, found)
		}
		sort.Strings(capability.Missing)
		result[feature] = capability
	}
	return result
}
