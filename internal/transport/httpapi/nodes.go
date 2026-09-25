package httpapi

import "net/http"

type nodeResponse struct {
	ID                string `json:"id"`
	GPUType           string `json:"gpu_type"`
	TotalGPUs         int64  `json:"total_gpus"`
	AvailableGPUs     int64  `json:"available_gpus"`
	TotalCPUs         int64  `json:"total_cpus"`
	AvailableCPUs     int64  `json:"available_cpus"`
	TotalMemoryGB     int64  `json:"total_memory_gb"`
	AvailableMemoryGB int64  `json:"available_memory_gb"`
	Healthy           bool   `json:"healthy"`
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.service.Nodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := make([]nodeResponse, 0, len(nodes))
	for _, node := range nodes {
		response = append(response, nodeResponse{
			ID:                node.ID,
			GPUType:           node.GPUType,
			TotalGPUs:         node.TotalGPUs,
			AvailableGPUs:     node.AvailableGPUs,
			TotalCPUs:         node.TotalCPUs,
			AvailableCPUs:     node.AvailableCPUs,
			TotalMemoryGB:     node.TotalMemoryGB,
			AvailableMemoryGB: node.AvailableMemoryGB,
			Healthy:           s.service.NodeHealthy(node.ID),
		})
	}

	writeJSON(w, http.StatusOK, response)
}
