package cases

import (
	"log"
	"net/http"

	"github.com/caiohperlin/perlinadv/internal/response"
)

type handler struct {
	service *service
}

func NewHandler(service *service) *handler {
	return &handler{
		service: service,
	}
}

func (h *handler) FindCases(w http.ResponseWriter, r *http.Request) {
	err := h.service.FindCases(r.Context())
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response.JSON(w, http.StatusOK, "whatevers")
}
