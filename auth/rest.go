package auth

/*
 * Microservice gateway application
 * Copyright (C) 2015  Martin Helmich <m.helmich@mittwald.de>
 *                     Mittwald CM Service GmbH & Co. KG
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

import (
	"github.com/go-zoo/bone"
	"github.com/mittwald/servicegateway/config"
	"net/http"
)

type RestAuthDecorator struct {
	authHandler *AuthenticationHandler
}

func (a *RestAuthDecorator) DecorateHandler(orig http.Handler, appCfg *config.Application) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		authenticated, _, err := a.authHandler.IsAuthenticated(req)
		if err != nil {
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(503)
			res.Write([]byte("{\"msg\": \"service unavailable\"}"))
		} else if !authenticated {
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(403)
			res.Write([]byte("{\"msg\": \"not authenticated\"}"))
		} else {
			orig.ServeHTTP(res, req)
		}
	})
}

func (a *RestAuthDecorator) RegisterRoutes(mux *bone.Mux) error {
	return nil
}
