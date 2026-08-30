package application

import (
	"log"
	"net/http"

	"github.com/caiohperlin/perlinadv/internal/config"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Application struct {
	router *chi.Mux
	server *http.Server
	config config.Config
}

func New(cfg config.Config) *Application {
	app := &Application{
		router: chi.NewRouter(),
		config: cfg,
	}

	app.mountMiddleware()
	app.mountRoutes()

	app.server = &http.Server{
		Addr:              cfg.Address,
		Handler:           app.router,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	return app
}

func (app *Application) mountMiddleware() {
	r := app.router
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
}

func (app *Application) mountRoutes() {
	r := app.router
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hallo"))
	})
}

func (app *Application) Run() error {
	log.Printf("Server has started at addr %s", app.server.Addr)
	return app.server.ListenAndServe()
}
