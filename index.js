/*
 * Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
 * or more contributor license agreements. Licensed under the Elastic License;
 * you may not use this file except in compliance with the Elastic License.
 */

import path from 'path';

export default function (kibana) {
    return new kibana.Plugin({
        id: 'go-langserver',
        require: ['elasticsearch', 'kibana'],
        name: 'go-langserver',
        config(Joi) {
            return Joi.object({
                enabled: Joi.boolean().default(true),
            }).default();
        },
        init(server) {
            server.expose('install', {
                path: path.join(__dirname, 'lib'),
            });
        }
    });
}
